package agentflow

import (
	"encoding/json"
	"strconv"
)

// quoteJSONString encodes s as a JSON string value without fmt.Sprintf.
func quoteJSONString(s string) json.RawMessage {
	return json.RawMessage(strconv.Quote(s))
}
