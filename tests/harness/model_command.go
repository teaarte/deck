package harness

import (
	"encoding/json"
)

// ModelManifest is a project with commands of type model: a fact check by a schema, a picture, speech and the
// words of a recording.
const ModelManifest = `{"version": 13,
  "commands": [
    {"name": "factcheck", "title": "Fact check", "description": "Checks a claim.", "class": "internal",
     "input": {"type": "object", "properties": {"claim": {"type": "string"}}, "required": ["claim"]},
     "result": {"type": "object", "properties": {"verdict": {"enum": ["true", "false", "unclear"]}, "confidence": {"type": "number"}},
       "required": ["verdict", "confidence"], "additionalProperties": false},
     "executor": {"type": "model", "kind": "text", "prompt": "Check the claim against what is known."}},
    {"name": "picture", "title": "Picture", "description": "Draws a scene.", "class": "internal",
     "input": {"type": "object", "properties": {"scene": {"type": "string"}}},
     "executor": {"type": "model", "kind": "image", "prompt": "Draw the scene, no text in the picture.", "options": {"image_config": {"aspect_ratio": "9:16"}}}},
    {"name": "voice", "title": "Voice", "description": "Speaks a line.", "class": "internal",
     "input": {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]},
     "executor": {"type": "model", "kind": "tts", "prompt": "Calm, like a documentary.", "options": {"voice": "onyx", "model": "an-ungranted-model"}}},
    {"name": "words", "title": "Words", "description": "Writes down a recording.", "class": "internal",
     "input": {"type": "object", "properties": {"voice": {"type": "string", "format": "file"}}, "required": ["voice"]},
     "executor": {"type": "model", "kind": "stt", "options": {"timestamp_granularities[]": "word"}}},
    {"name": "track", "title": "Track", "description": "Makes background music.", "class": "internal",
     "input": {"type": "object", "properties": {"mood": {"type": "string"}}},
     "executor": {"type": "model", "kind": "music", "prompt": "Instrumental background music, no vocals.", "options": {"seconds": 40}}}
  ]}`

func ChatText(content string) string {
	b, _ := json.Marshal(map[string]any{"id": "c-1", "model": "fact-model", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}},
		"usage": map[string]any{"prompt_tokens": 200, "completion_tokens": 20}})
	return string(b)
}
