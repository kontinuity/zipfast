package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lucsky/cuid"

	"zipfast/internal/auth"
	"zipfast/internal/datasource"
	"zipfast/internal/media"
	"zipfast/internal/models"
	"zipfast/internal/parser"
	"zipfast/internal/upload"
	"zipfast/internal/webhooks"
)

// upload_routes.go implements the upload pipeline: POST /api/upload (streaming
// multipart, one temp file per part, then datasource.Put) and
// POST /api/upload/partial (chunked uploads assembled in the temp directory).
//
// Memory discipline: file parts are never buffered whole in memory. Each part is
// streamed to a temp file under cfg.Core.TempDirectory, and the datasource is
// fed from that temp file. The only time bytes live in memory is when image
// compression is requested (the compressor returns the re-encoded bytes), which
// is bounded by the image dimensions and explicitly opt-in.

// uploadFileResult is one entry in the JSON upload response.
type uploadFileResult struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	URL  string `json:"url"`
}

// uploadResponse is the JSON body returned by a successful (non-noJSON) upload.
type uploadResponse struct {
	Files     []uploadFileResult `json:"files"`
	DeletesAt string             `json:"deletesAt,omitempty"`
}

// registerUploadRoutes wires the upload endpoints onto r.
func (a *App) registerUploadRoutes(r chi.Router) {
	r.Post("/api/upload", a.handleUpload)
	r.Post("/api/upload/partial", a.handleUploadPartial)
}

// handleUpload streams a multipart/form-data upload, storing each file part and
// recording it in the database. See the package-level note on memory discipline.
func (a *App) handleUpload(w http.ResponseWriter, r *http.Request) {
	log := a.logFor(r)
	user := a.authenticate(r)

	// Parse the x-zipline-* directives. ParseHeaders only needs the header set
	// and the files config.
	opts, err := upload.ParseHeaders(r.Header, a.Cfg.Files)
	if err != nil {
		a.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	// The dedicated partial endpoint handles chunked uploads; reject them here.
	if opts.Partial != nil {
		a.Error(w, http.StatusBadRequest, "bad options, received: partial upload")
		return
	}

	// Resolve the destination folder (if any) and decide whether an anonymous
	// upload is permitted.
	var folder *models.Folder
	if opts.Folder != "" {
		folder, err = a.uploadGetFolder(r, opts.Folder)
		if err != nil {
			a.Error(w, http.StatusNotFound, "folder not found")
			return
		}
		if user == nil && !folder.AllowUploads {
			a.Error(w, http.StatusForbidden, "anonymous uploads are not allowed to this folder")
			return
		}
	}

	// Authentication requirement: a request with no user is only allowed when it
	// targets a folder that permits anonymous uploads.
	anonymousFolderUpload := user == nil && folder != nil && folder.AllowUploads
	if user == nil && !anonymousFolderUpload {
		a.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	log.Debug("upload started", "folder", opts.Folder, "anonymous", anonymousFolderUpload)

	mr, err := r.MultipartReader()
	if err != nil {
		a.Error(w, http.StatusBadRequest, "expected multipart/form-data request")
		return
	}

	if err := os.MkdirAll(a.Cfg.Core.TempDirectory, 0o755); err != nil {
		a.Error(w, http.StatusInternalServerError, "could not prepare temp directory")
		return
	}

	maxFileSize, _ := upload.ParseBytes(a.Cfg.Files.MaxFileSize)

	results := make([]uploadFileResult, 0, 4)
	fileCount := 0

	for {
		part, perr := mr.NextPart()
		if perr == io.EOF {
			break
		}
		if perr != nil {
			a.Error(w, http.StatusBadRequest, "malformed multipart request")
			return
		}

		// Only file parts (those with a filename) are uploads. Skip plain form
		// fields.
		if part.FileName() == "" {
			_ = part.Close()
			continue
		}

		fileCount++
		if a.Cfg.Files.MaxFilesPerUpload > 0 && fileCount > a.Cfg.Files.MaxFilesPerUpload {
			_ = part.Close()
			a.Error(w, http.StatusBadRequest, fmt.Sprintf("too many files: maximum is %d", a.Cfg.Files.MaxFilesPerUpload))
			return
		}

		res, herr := a.uploadProcessPart(r, part, opts, user, folder, anonymousFolderUpload, maxFileSize)
		_ = part.Close()
		if herr != nil {
			a.Error(w, herr.status, herr.msg)
			return
		}
		results = append(results, res)
	}

	if len(results) == 0 {
		a.Error(w, http.StatusBadRequest, "no files received")
		return
	}

	log.Info("upload complete", "files", len(results), "folder", opts.Folder, "anonymous", anonymousFolderUpload)

	// noJSON: a plain-text, comma-separated list of URLs.
	if opts.NoJSON {
		urls := make([]string, len(results))
		for i, f := range results {
			urls[i] = f.URL
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, strings.Join(urls, ","))
		return
	}

	resp := uploadResponse{Files: results}
	if opts.DeletesAt != nil {
		resp.DeletesAt = opts.DeletesAt.Format(time.RFC3339)
	}
	a.WriteJSON(w, http.StatusOK, resp)
}

// uploadError carries an HTTP status and message out of the per-part helper.
type uploadError struct {
	status int
	msg    string
}

func (e *uploadError) Error() string { return e.msg }

// uploadProcessPart streams one file part to a temp file, applies the requested
// transforms (compression, GPS strip), stores it in the datasource, records the
// file row, fires webhooks, and returns the response entry.
func (a *App) uploadProcessPart(
	r *http.Request,
	part *multipart.Part,
	opts *upload.Options,
	user *models.User,
	folder *models.Folder,
	anonymousFolderUpload bool,
	maxFileSize int64,
) (uploadFileResult, *uploadError) {
	partFilename := part.FileName()

	// Stream the part to a temp file (bounded by maxFileSize) so large uploads
	// never sit in memory.
	tmpPath, written, terr := a.uploadStreamToTemp(part, maxFileSize)
	if tmpPath != "" {
		defer os.Remove(tmpPath)
	}
	if terr != nil {
		return uploadFileResult{}, terr
	}

	// Enforce the uploader's storage/file quota (no-op for anonymous or unlimited).
	if user != nil {
		if qerr := a.EnforceFileQuota(r.Context(), user.ID, written, 1); qerr != nil {
			return uploadFileResult{}, &uploadError{http.StatusForbidden, qerr.Error()}
		}
	}

	// Derive the extension. Zipline's getExtension returns extname() which
	// includes the leading dot; an override is the bare extension, so we re-add
	// the dot. ExtensionlessUrls drops it entirely.
	ext := uploadExtension(partFilename, opts.OverrideExtension)

	// Disabled-extension guard (compares with the dot, as Zipline stores it).
	if uploadExtDisabled(ext, a.Cfg.Files.DisabledExtensions) {
		return uploadFileResult{}, &uploadError{http.StatusBadRequest, "file extension " + ext + " is not allowed"}
	}

	// Choose the output base name.
	format := opts.Format
	if format == "" {
		format = a.Cfg.Files.DefaultFormat
	}
	var baseName string
	if opts.OverrideFilename != "" {
		baseName = upload.SanitizeFilename(opts.OverrideFilename)
	} else {
		n, ferr := upload.FormatFileName(format, partFilename, a.Cfg.Files)
		if ferr != nil {
			return uploadFileResult{}, &uploadError{http.StatusBadRequest, "could not generate file name: " + ferr.Error()}
		}
		baseName = n
	}
	if baseName == "" {
		return uploadFileResult{}, &uploadError{http.StatusBadRequest, "invalid file name"}
	}

	// Detect the content type: prefer the part's Content-Type, else guess from
	// the extension.
	contentType := uploadDetectContentType(part, ext)

	// The bytes/size we will ultimately store. By default we stream from the
	// temp file; compression replaces this with an in-memory buffer.
	storeFromPath := tmpPath
	storeBytes := []byte(nil)
	storeSize := written

	// Optional image compression (opt-in via headers). Only for image types.
	if opts.Compression != nil && strings.HasPrefix(contentType, "image/") {
		data, mimeType, cext, cerr := media.Compress(tmpPath, opts.Compression.Type, opts.Compression.Percent)
		if cerr != nil {
			a.Log.Warn("upload: compression failed, using original", "err", cerr)
		} else {
			storeBytes = data
			storeFromPath = ""
			storeSize = int64(len(data))
			contentType = mimeType
			// The compressed result dictates the extension (with a dot).
			ext = "." + strings.TrimPrefix(cext, ".")
		}
	}

	// Optional GPS metadata strip (best-effort, in place on the temp file). Only
	// meaningful when we are still serving from the temp file (not compressed).
	if a.Cfg.Files.RemoveGPSMetadata && storeFromPath != "" && strings.HasPrefix(contentType, "image/") {
		if _, gerr := media.StripGPS(storeFromPath); gerr != nil {
			a.Log.Debug("upload: gps strip failed", "err", gerr)
		} else if fi, sterr := os.Stat(storeFromPath); sterr == nil {
			storeSize = fi.Size()
		}
	}

	// Compose the stored name (the URL slug).
	storedName := baseName
	if !a.Cfg.Files.ExtensionlessUrls {
		storedName = baseName + ext
	}

	// Store the bytes in the datasource.
	if serr := a.uploadPutObject(storedName, storeFromPath, storeBytes, storeSize, contentType); serr != nil {
		a.Log.Error("upload: datasource put failed", "name", storedName, "err", serr)
		return uploadFileResult{}, &uploadError{http.StatusInternalServerError, "failed to store file"}
	}

	// Build and insert the file row.
	file, ierr := a.uploadInsertFile(r, storedName, partFilename, storeSize, contentType, opts, user, folder, anonymousFolderUpload)
	if ierr != nil {
		// Roll back the stored object so we don't leak orphans.
		_ = a.DS.Delete(storedName)
		a.Log.Error("upload: insert file row failed", "name", storedName, "err", ierr)
		return uploadFileResult{}, &uploadError{http.StatusInternalServerError, "failed to record file"}
	}

	url := a.BaseURL(r) + uploadFilesRoutePrefix(a.Cfg.Files.Route) + "/" + storedName

	// Fire webhooks (fire-and-forget; the package detaches its own goroutines).
	hookUser := user
	if hookUser == nil {
		hookUser = &models.User{ID: "anonymous", Username: "anonymous", Role: models.RoleUser}
	}
	go webhooks.OnUpload(a.Cfg, file, hookUser, parser.Link{
		Returned: url,
		Raw:      a.BaseURL(r) + "/raw/" + storedName,
	})

	return uploadFileResult{ID: file.ID, Name: file.Name, Type: file.Type, URL: url}, nil
}

// uploadStreamToTemp copies part into a fresh temp file under the configured temp
// directory, enforcing maxFileSize (0 = unlimited). It returns the temp path and
// the number of bytes written.
func (a *App) uploadStreamToTemp(part io.Reader, maxFileSize int64) (string, int64, *uploadError) {
	tmp, err := os.CreateTemp(a.Cfg.Core.TempDirectory, "zipfast-upload-*")
	if err != nil {
		return "", 0, &uploadError{http.StatusInternalServerError, "could not create temp file"}
	}
	tmpPath := tmp.Name()

	var reader io.Reader = part
	if maxFileSize > 0 {
		// Allow one extra byte so we can detect "too large".
		reader = io.LimitReader(part, maxFileSize+1)
	}

	written, cerr := io.Copy(tmp, reader)
	closeErr := tmp.Close()
	if cerr != nil {
		return tmpPath, written, &uploadError{http.StatusInternalServerError, "failed to read uploaded file"}
	}
	if closeErr != nil {
		return tmpPath, written, &uploadError{http.StatusInternalServerError, "failed to write uploaded file"}
	}
	if maxFileSize > 0 && written > maxFileSize {
		return tmpPath, written, &uploadError{http.StatusRequestEntityTooLarge, fmt.Sprintf("file is too large; maximum is %d bytes", maxFileSize)}
	}
	return tmpPath, written, nil
}

// uploadPutObject stores either the bytes in buf (when non-nil) or the contents
// of srcPath into the datasource under name.
func (a *App) uploadPutObject(name, srcPath string, buf []byte, size int64, contentType string) error {
	opts := datasource.PutOptions{Mimetype: contentType}
	if buf != nil {
		return a.DS.Put(name, bytes.NewReader(buf), int64(len(buf)), opts)
	}
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return a.DS.Put(name, f, size, opts)
}

// uploadInsertFile inserts the files row and returns the populated model.
func (a *App) uploadInsertFile(
	r *http.Request,
	storedName, partFilename string,
	size int64,
	contentType string,
	opts *upload.Options,
	user *models.User,
	folder *models.Folder,
	anonymousFolderUpload bool,
) (*models.File, error) {
	id := cuid.New()

	// Owner: the authenticated user, or (for an anonymous folder upload) the
	// folder's owner.
	var userID *string
	if user != nil {
		userID = &user.ID
	} else if folder != nil {
		owner := folder.UserID
		userID = &owner
	}

	var passwordHash *string
	if opts.Password != "" {
		h, err := auth.HashPassword(opts.Password)
		if err != nil {
			return nil, err
		}
		passwordHash = &h
	}

	var originalName *string
	if opts.AddOriginalName {
		og := upload.SanitizeFilename(partFilename)
		if og != "" {
			originalName = &og
		}
	}

	var folderID *string
	if folder != nil {
		folderID = &folder.ID
	}

	anonymous := anonymousFolderUpload

	now := time.Now()
	file := &models.File{
		ID:           id,
		CreatedAt:    now,
		UpdatedAt:    now,
		DeletesAt:    opts.DeletesAt,
		Name:         storedName,
		OriginalName: originalName,
		Size:         size,
		Type:         contentType,
		MaxViews:     opts.MaxViews,
		Password:     passwordHash,
		Anonymous:    anonymous,
		UserID:       userID,
		FolderID:     folderID,
	}

	_, err := a.Store.Pool.Exec(r.Context(),
		`INSERT INTO files (id, created_at, updated_at, deletes_at, name, original_name, size, type,
		                    max_views, password, anonymous, user_id, folder_id)
		 VALUES ($1, now(), now(), $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		file.ID, file.DeletesAt, file.Name, file.OriginalName, file.Size, file.Type,
		file.MaxViews, file.Password, file.Anonymous, file.UserID, file.FolderID)
	if err != nil {
		return nil, err
	}
	return file, nil
}

// uploadGetFolder loads a folder by id.
func (a *App) uploadGetFolder(r *http.Request, id string) (*models.Folder, error) {
	var f models.Folder
	err := a.Store.Pool.QueryRow(r.Context(),
		`SELECT id, created_at, updated_at, name, public, allow_uploads, parent_id, user_id
		 FROM folders WHERE id=$1`, id).
		Scan(&f.ID, &f.CreatedAt, &f.UpdatedAt, &f.Name, &f.Public, &f.AllowUploads, &f.ParentID, &f.UserID)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// ---------------------------------------------------------------------------
// Partial / chunked uploads
// ---------------------------------------------------------------------------

// handleUploadPartial implements chunked uploads. Each request carries one chunk
// (a multipart file part) plus the x-zipline-p-* / content-range headers parsed
// into opts.Partial. Chunks are written to per-(identifier,start) temp files;
// on the last chunk they are assembled in offset order and stored as a single
// object, then recorded as a file.
func (a *App) handleUploadPartial(w http.ResponseWriter, r *http.Request) {
	user := a.authenticate(r)

	opts, err := upload.ParseHeaders(r.Header, a.Cfg.Files)
	if err != nil {
		a.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if opts.Partial == nil {
		a.Error(w, http.StatusBadRequest, "missing partial upload headers")
		return
	}

	var folder *models.Folder
	if opts.Folder != "" {
		folder, err = a.uploadGetFolder(r, opts.Folder)
		if err != nil {
			a.Error(w, http.StatusNotFound, "folder not found")
			return
		}
		if user == nil && !folder.AllowUploads {
			a.Error(w, http.StatusForbidden, "anonymous uploads are not allowed to this folder")
			return
		}
	}

	anonymousFolderUpload := user == nil && folder != nil && folder.AllowUploads
	if user == nil && !anonymousFolderUpload {
		a.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	identifier := opts.Partial.Identifier
	if identifier == "" {
		a.Error(w, http.StatusBadRequest, "missing partial identifier")
		return
	}
	// Keep the identifier filesystem-safe: it becomes part of temp file names.
	if upload.SanitizeFilename(identifier) != identifier || strings.Contains(identifier, "_") {
		a.Error(w, http.StatusBadRequest, "invalid partial identifier")
		return
	}

	if err := os.MkdirAll(a.Cfg.Core.TempDirectory, 0o755); err != nil {
		a.Error(w, http.StatusInternalServerError, "could not prepare temp directory")
		return
	}

	// Read the single chunk part out of the multipart body.
	mr, err := r.MultipartReader()
	if err != nil {
		a.Error(w, http.StatusBadRequest, "expected multipart/form-data request")
		return
	}
	var chunkPart *multipart.Part
	for {
		p, perr := mr.NextPart()
		if perr == io.EOF {
			break
		}
		if perr != nil {
			a.Error(w, http.StatusBadRequest, "malformed multipart request")
			return
		}
		if p.FileName() != "" {
			chunkPart = p
			break
		}
		_ = p.Close()
	}
	if chunkPart == nil {
		a.Error(w, http.StatusBadRequest, "no chunk received")
		return
	}

	// Write this chunk to its own temp file keyed by identifier + byte range.
	chunkName := fmt.Sprintf("zipline_partial_%s_%d_%d", identifier, opts.Partial.Start, opts.Partial.End)
	chunkPath := filepath.Join(a.Cfg.Core.TempDirectory, chunkName)
	if werr := uploadWriteFile(chunkPath, chunkPart); werr != nil {
		_ = chunkPart.Close()
		a.Error(w, http.StatusInternalServerError, "failed to write chunk")
		return
	}
	_ = chunkPart.Close()

	resp := map[string]any{
		"files":          []uploadFileResult{},
		"partialSuccess": true,
	}
	if opts.Partial.Start == 0 {
		resp["partialIdentifier"] = identifier
	}

	if !opts.Partial.Lastchunk {
		a.WriteJSON(w, http.StatusOK, resp)
		return
	}

	// Final chunk: assemble all chunks for this identifier in offset order.
	assembledPath, total, aerr := a.uploadAssembleChunks(identifier)
	if aerr != nil {
		a.Error(w, http.StatusInternalServerError, "failed to assemble chunks")
		return
	}
	defer os.Remove(assembledPath)

	// The total size is authoritative from the content-range total when present.
	if opts.Partial.Total > 0 {
		total = opts.Partial.Total
	}

	// Derive name/extension/type from the partial metadata.
	ext := uploadExtension(opts.Partial.Filename, opts.OverrideExtension)
	if uploadExtDisabled(ext, a.Cfg.Files.DisabledExtensions) {
		a.Error(w, http.StatusBadRequest, "file extension "+ext+" is not allowed")
		return
	}

	format := opts.Format
	if format == "" {
		format = a.Cfg.Files.DefaultFormat
	}
	var baseName string
	if opts.OverrideFilename != "" {
		baseName = upload.SanitizeFilename(opts.OverrideFilename)
	} else {
		n, ferr := upload.FormatFileName(format, opts.Partial.Filename, a.Cfg.Files)
		if ferr != nil {
			a.Error(w, http.StatusBadRequest, "could not generate file name: "+ferr.Error())
			return
		}
		baseName = n
	}
	if baseName == "" {
		a.Error(w, http.StatusBadRequest, "invalid file name")
		return
	}

	contentType := opts.Partial.ContentType
	if contentType == "" || contentType == "application/octet-stream" {
		if guessed := uploadGuessByExt(ext); guessed != "" {
			contentType = guessed
		}
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	storedName := baseName
	if !a.Cfg.Files.ExtensionlessUrls {
		storedName = baseName + ext
	}

	if serr := a.uploadPutObject(storedName, assembledPath, nil, total, contentType); serr != nil {
		a.Log.Error("partial upload: datasource put failed", "name", storedName, "err", serr)
		a.Error(w, http.StatusInternalServerError, "failed to store file")
		return
	}

	// Record the file. Partial uploads reuse the same insert helper; the partial
	// filename stands in for the original name source.
	pf := &upload.Options{
		DeletesAt:       opts.DeletesAt,
		MaxViews:        opts.MaxViews,
		Password:        opts.Password,
		AddOriginalName: opts.AddOriginalName,
		Folder:          opts.Folder,
	}
	file, ierr := a.uploadInsertFile(r, storedName, opts.Partial.Filename, total, contentType, pf, user, folder, anonymousFolderUpload)
	if ierr != nil {
		_ = a.DS.Delete(storedName)
		a.Log.Error("partial upload: insert file row failed", "name", storedName, "err", ierr)
		a.Error(w, http.StatusInternalServerError, "failed to record file")
		return
	}

	url := a.BaseURL(r) + uploadFilesRoutePrefix(a.Cfg.Files.Route) + "/" + storedName

	hookUser := user
	if hookUser == nil {
		hookUser = &models.User{ID: "anonymous", Username: "anonymous", Role: models.RoleUser}
	}
	go webhooks.OnUpload(a.Cfg, file, hookUser, parser.Link{
		Returned: url,
		Raw:      a.BaseURL(r) + "/raw/" + storedName,
	})

	resp["files"] = []uploadFileResult{{ID: file.ID, Name: file.Name, Type: file.Type, URL: url}}
	a.WriteJSON(w, http.StatusOK, resp)
}

// uploadAssembleChunks concatenates every chunk temp file for identifier in
// ascending start-offset order into a single new temp file. It removes the chunk
// files as it goes and returns the assembled path and total byte count.
func (a *App) uploadAssembleChunks(identifier string) (string, int64, error) {
	prefix := fmt.Sprintf("zipline_partial_%s_", identifier)

	entries, err := os.ReadDir(a.Cfg.Core.TempDirectory)
	if err != nil {
		return "", 0, err
	}

	type chunkRef struct {
		path  string
		start int64
	}
	var chunks []chunkRef
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		// name: zipline_partial_{identifier}_{start}_{end}
		rest := strings.TrimPrefix(e.Name(), prefix)
		fields := strings.SplitN(rest, "_", 2)
		start, perr := strconv.ParseInt(fields[0], 10, 64)
		if perr != nil {
			continue
		}
		chunks = append(chunks, chunkRef{path: filepath.Join(a.Cfg.Core.TempDirectory, e.Name()), start: start})
	}
	if len(chunks) == 0 {
		return "", 0, errors.New("no chunks found for identifier")
	}
	sort.Slice(chunks, func(i, j int) bool { return chunks[i].start < chunks[j].start })

	out, err := os.CreateTemp(a.Cfg.Core.TempDirectory, "zipfast-assembled-*")
	if err != nil {
		return "", 0, err
	}
	outPath := out.Name()

	var total int64
	for _, c := range chunks {
		in, oerr := os.Open(c.path)
		if oerr != nil {
			out.Close()
			os.Remove(outPath)
			return "", 0, oerr
		}
		n, cerr := io.Copy(out, in)
		in.Close()
		_ = os.Remove(c.path)
		if cerr != nil {
			out.Close()
			os.Remove(outPath)
			return "", 0, cerr
		}
		total += n
	}
	if cerr := out.Close(); cerr != nil {
		os.Remove(outPath)
		return "", 0, cerr
	}
	return outPath, total, nil
}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

// uploadWriteFile streams r into a new file at dst.
func uploadWriteFile(dst string, r io.Reader) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

// uploadExtension returns the file extension to use, including a leading dot.
// An override (the bare extension, already sanitized) wins; otherwise the
// extension is taken from the original filename. The result is "" when there is
// no extension.
func uploadExtension(filename, override string) string {
	if override != "" {
		return "." + strings.TrimPrefix(override, ".")
	}
	return path.Ext(filename)
}

// uploadExtDisabled reports whether ext (with leading dot) is in the disabled
// list. The list may contain entries with or without a leading dot.
func uploadExtDisabled(ext string, disabled []string) bool {
	if ext == "" {
		return false
	}
	bare := strings.TrimPrefix(strings.ToLower(ext), ".")
	for _, d := range disabled {
		if strings.TrimPrefix(strings.ToLower(strings.TrimSpace(d)), ".") == bare {
			return true
		}
	}
	return false
}

// uploadDetectContentType resolves the content type for a part: the part's own
// Content-Type header if it is set and specific, else a guess from the
// extension, else application/octet-stream.
func uploadDetectContentType(part *multipart.Part, ext string) string {
	ct := part.Header.Get("Content-Type")
	ct = strings.TrimSpace(ct)
	if ct != "" && ct != "application/octet-stream" {
		return ct
	}
	if guessed := uploadGuessByExt(ext); guessed != "" {
		return guessed
	}
	if ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// uploadGuessByExt guesses a MIME type from a file extension (with or without a
// leading dot) using the standard library's type table.
func uploadGuessByExt(ext string) string {
	if ext == "" {
		return ""
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	t := mime.TypeByExtension(ext)
	if t == "" {
		return ""
	}
	// Strip any "; charset=..." suffix so the stored type is the bare MIME type.
	if i := strings.IndexByte(t, ';'); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	return t
}

// uploadFilesRoutePrefix normalizes the files route into a URL prefix. A route
// of "/" or "" yields no prefix (files served from the root); otherwise the
// route is returned trimmed of any trailing slash.
func uploadFilesRoutePrefix(route string) string {
	if route == "" || route == "/" {
		return ""
	}
	return strings.TrimRight(route, "/")
}
