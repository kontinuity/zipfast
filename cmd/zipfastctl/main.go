// Command zipfastctl is an administrative CLI for Zipfast. It reuses the server's
// config loader and database layer so operators can inspect the effective
// configuration and manage users without touching SQL directly.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"zipfast/internal/auth"
	"zipfast/internal/config"
	"zipfast/internal/db"
)

// version is overridable at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "zipfastctl",
		Short:         "Administrative CLI for Zipfast",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		readConfigCmd(),
		listUsersCmd(),
		setUserCmd(),
		setFolderPasswordCmd(),
		versionCmd(),
	)
	return root
}

// readConfigCmd prints the effective configuration (defaults overlaid with env)
// as indented JSON.
func readConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "read-config",
		Short: "Load and print the effective configuration as JSON",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			out, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return nil
		},
	}
}

// listUsersCmd prints all users in a tabular form.
func listUsersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list-users",
		Short: "List all users",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			ctx := context.Background()
			store, err := db.New(ctx, cfg.Core.DatabaseURL)
			if err != nil {
				return err
			}
			defer store.Close()

			rows, err := store.Pool.Query(ctx,
				`SELECT id, username, role, created_at FROM users ORDER BY created_at`)
			if err != nil {
				return err
			}
			defer rows.Close()

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tUSERNAME\tROLE\tCREATED_AT")

			count := 0
			for rows.Next() {
				var id, username, role string
				var createdAt time.Time
				if err := rows.Scan(&id, &username, &role, &createdAt); err != nil {
					return err
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", id, username, role, createdAt.Format(time.RFC3339))
				count++
			}
			if err := rows.Err(); err != nil {
				return err
			}
			if err := w.Flush(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\n%d user(s)\n", count)
			return nil
		},
	}
}

// setUserCmd updates a single allowed field on a user.
func setUserCmd() *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:   "set-user --id <id> <field> <value>",
		Short: "Update a single field (role|username|password) on a user",
		Long: "Update a single allowed field on a user.\n\n" +
			"Allowed fields:\n" +
			"  role      one of USER, ADMIN, SUPERADMIN\n" +
			"  username  the new username\n" +
			"  password  the new password (hashed before storing)",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id is required")
			}
			field, value := args[0], args[1]

			var column string
			switch field {
			case "role":
				switch value {
				case "USER", "ADMIN", "SUPERADMIN":
				default:
					return fmt.Errorf("invalid role %q: must be USER, ADMIN, or SUPERADMIN", value)
				}
				column = "role"
			case "username":
				if value == "" {
					return fmt.Errorf("username may not be empty")
				}
				column = "username"
			case "password":
				if value == "" {
					return fmt.Errorf("password may not be empty")
				}
				hashed, err := auth.HashPassword(value)
				if err != nil {
					return fmt.Errorf("hash password: %w", err)
				}
				value = hashed
				column = "password"
			default:
				return fmt.Errorf("unsupported field %q: allowed fields are role, username, password", field)
			}

			cfg, err := config.Load()
			if err != nil {
				return err
			}

			ctx := context.Background()
			store, err := db.New(ctx, cfg.Core.DatabaseURL)
			if err != nil {
				return err
			}
			defer store.Close()

			// Column is from a fixed whitelist above, so interpolation is safe.
			query := fmt.Sprintf(`UPDATE users SET %s=$1, updated_at=now() WHERE id=$2`, column)
			tag, err := store.Pool.Exec(ctx, query, value, id)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return fmt.Errorf("no user found with id %q", id)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "updated user %s: set %s\n", id, field)
			return nil
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "the id of the user to update (required)")
	return cmd
}

// setFolderPasswordCmd sets or clears the gate password on a folder, identified
// by id or name. Passing no password (or an empty one) clears it.
func setFolderPasswordCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-folder-password <folderId|name> [password]",
		Short: "Set or clear the password on a folder (empty clears)",
		Long: "Set or clear the gate password on a public folder.\n\n" +
			"Provide a password to protect the folder, or omit it (or pass an\n" +
			"empty string) to remove protection. The folder is matched by id or name.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ident := args[0]
			password := ""
			if len(args) == 2 {
				password = args[1]
			}

			var value any // nil clears the column
			if password != "" {
				hashed, err := auth.HashPassword(password)
				if err != nil {
					return fmt.Errorf("hash password: %w", err)
				}
				value = hashed
			}

			cfg, err := config.Load()
			if err != nil {
				return err
			}

			ctx := context.Background()
			store, err := db.New(ctx, cfg.Core.DatabaseURL)
			if err != nil {
				return err
			}
			defer store.Close()

			tag, err := store.Pool.Exec(ctx,
				`UPDATE folders SET password=$1, updated_at=now() WHERE id=$2 OR name=$2`,
				value, ident)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return fmt.Errorf("no folder found with id or name %q", ident)
			}

			if value == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "cleared password on folder %q\n", ident)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "set password on folder %q\n", ident)
			}
			return nil
		},
	}
	return cmd
}

// versionCmd prints the build version.
func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "zipfastctl %s\n", version)
		},
	}
}
