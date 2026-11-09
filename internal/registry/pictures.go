package registry

// Picture is a picture a result shows a model: the gateway gives it as an image beside the result's JSON.
type Picture struct {
	Type string
	Data []byte
}

// Pictured is a result with pictures for a model; its JSON names them by address for the other callers.
type Pictured interface{ Pictures() []Picture }
