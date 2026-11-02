package models

// Kinds are what a model of deck's settings makes; search is a search service such as SearXNG.
const (
	KindText   = "text"
	KindImage  = "image"
	KindTTS    = "tts"
	KindSTT    = "stt"
	KindMusic  = "music"
	KindVideo  = "video"
	KindSearch = "search"
)

// Units are what a price per million of a model counts: tokens in and out, characters deck sent, or seconds of a
// recording the API reported.
const (
	UnitToken  = "token"
	UnitChar   = "char"
	UnitSecond = "second"
)

// APICLI is the API of a connection deck keeps itself for an agent CLI on the owner's subscription: no address, no
// key, its models run agent turns in the CLI of its name.
const APICLI = "cli"
