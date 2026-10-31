package harness

import (
	"encoding/json"
)

func ChatText(content string) string {
	b, _ := json.Marshal(map[string]any{"id": "c-1", "model": "fact-model", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}},
		"usage": map[string]any{"prompt_tokens": 200, "completion_tokens": 20}})
	return string(b)
}
