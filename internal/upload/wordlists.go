package upload

// adjectives and nouns are the small, tasteful word lists used by the "gfycat"
// (a.k.a. "random-words") file-name format. A name is built by joining a
// configurable number of adjectives with a single noun, mirroring the
// gfycat-style identifiers used by the original Zipline.

// adjectives is a list of ~60 inoffensive descriptive words.
var adjectives = []string{
	"adorable", "agile", "ample", "amused", "ancient",
	"better", "brave", "breezy", "bright", "calm",
	"clever", "cosmic", "curious", "daring", "dapper",
	"eager", "elegant", "fancy", "fluffy", "fuzzy",
	"gentle", "giant", "glad", "golden", "graceful",
	"happy", "hidden", "humble", "icy", "jolly",
	"kind", "lively", "lucky", "lunar", "merry",
	"mighty", "misty", "noble", "polite", "proud",
	"quick", "quiet", "rapid", "royal", "rustic",
	"shiny", "silent", "silly", "smooth", "snug",
	"solar", "spry", "sturdy", "sunny", "swift",
	"tidy", "vivid", "warm", "witty", "zesty",
}

// nouns is a list of ~60 inoffensive animal names.
var nouns = []string{
	"antelope", "badger", "beaver", "bison", "buffalo",
	"camel", "cheetah", "chipmunk", "cobra", "cougar",
	"coyote", "crane", "dolphin", "donkey", "eagle",
	"falcon", "ferret", "finch", "fox", "gazelle",
	"gecko", "giraffe", "goose", "hamster", "hawk",
	"hedgehog", "heron", "ibex", "iguana", "jackal",
	"jaguar", "koala", "lemur", "leopard", "lizard",
	"llama", "lynx", "magpie", "marmot", "meerkat",
	"mongoose", "moose", "newt", "ocelot", "otter",
	"panda", "panther", "pelican", "penguin", "puffin",
	"quail", "rabbit", "raccoon", "raven", "salmon",
	"sparrow", "tiger", "toucan", "walrus", "weasel",
}
