package packageops

import (
	"bytes"
	"encoding/json"
	"fmt"
)

func decodeCommandJSON(data []byte, destination any) error {
	start := bytes.IndexByte(data, '{')
	end := bytes.LastIndexByte(data, '}')
	if start < 0 || end < start {
		return fmt.Errorf("command output contained no JSON document")
	}
	if err := json.Unmarshal(data[start:end+1], destination); err != nil {
		return fmt.Errorf("decode JSON document: %w", err)
	}
	return nil
}
