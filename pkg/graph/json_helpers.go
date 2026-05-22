package graph

import (
	"encoding/json"
	"fmt"
	"strings"
)

func rawJSONLiteral(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "json.RawMessage(`{}`)"
	}
	escaped := strings.ReplaceAll(string(raw), "`", "` + \"`\" + `")
	return fmt.Sprintf("json.RawMessage(`%s`)", escaped)
}
