package server

import "encoding/json"

// embeddedBaseRespStatus extracts base_resp.status_code from a response body
// that uses the MiniMax-style error envelope (HTTP 200 with the failure
// embedded in the JSON body instead of the status line). Returns 0 when the
// body is not JSON, has no base_resp object, or reports success
// (base_resp.status_code == 0), which is the normal case for successful
// MiniMax responses.
func embeddedBaseRespStatus(body []byte) int {
	var env struct {
		BaseResp struct {
			StatusCode int `json:"status_code"`
		} `json:"base_resp"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return 0
	}
	return env.BaseResp.StatusCode
}
