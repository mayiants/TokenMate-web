// Package notify delivers notification text to IM group-bot webhooks
// (Feishu / DingTalk / WeCom). Each platform has its own payload shape and
// signing scheme; this package normalizes them behind one Send call.
// See docs/notification-system-proposal-202610312238.md §6.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Channel types.
const (
	TypeFeishu   = "feishu"
	TypeDingTalk = "dingtalk"
	TypeWeCom    = "wecom"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

// Send posts one message to the given webhook. title is used where the
// platform requires a separate title field (DingTalk markdown); text is the
// message body. The secret signs the payload for feishu/dingtalk and is
// ignored for wecom (whose group bots are URL-only).
func Send(ctx context.Context, channelType, webhookURL, secret, title, text string) error {
	switch channelType {
	case TypeFeishu:
		return sendFeishu(ctx, webhookURL, secret, text)
	case TypeDingTalk:
		return sendDingTalk(ctx, webhookURL, secret, title, text)
	case TypeWeCom:
		return sendWeCom(ctx, webhookURL, text)
	default:
		return fmt.Errorf("unknown channel type: %s", channelType)
	}
}

// sign computes the shared Feishu/DingTalk HMAC-SHA256 signature:
// base64(HMAC-SHA256(key=timestamp+"\n"+secret, empty)).
func sign(timestamp int64, secret string) string {
	mac := hmac.New(sha256.New, []byte(strconv.FormatInt(timestamp, 10)+"\n"+secret))
	mac.Write([]byte{})
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func postJSON(ctx context.Context, url string, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webhook HTTP %d: %s", resp.StatusCode, string(body))
	}
	// Feishu/DingTalk return 200 with an error code inside the JSON body.
	var ack struct {
		Code int    `json:"code"`
		Err  int    `json:"errcode"`
		Msg  string `json:"msg"`
	}
	if json.Unmarshal(body, &ack) == nil {
		if ack.Code != 0 {
			return fmt.Errorf("feishu error code %d: %s", ack.Code, ack.Msg)
		}
		if ack.Err != 0 {
			return fmt.Errorf("dingtalk errcode %d: %s", ack.Err, ack.Msg)
		}
	}
	return nil
}

// sendFeishu posts a text message to a Feishu custom-bot webhook. Signing is
// optional in Feishu; when a secret is configured we send timestamp+sign.
func sendFeishu(ctx context.Context, webhookURL, secret, text string) error {
	timestamp := time.Now().Unix()
	payload := map[string]interface{}{
		"msg_type": "text",
		"content":  map[string]string{"text": text},
	}
	if secret != "" {
		payload["timestamp"] = strconv.FormatInt(timestamp, 10)
		payload["sign"] = sign(timestamp, secret)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return postJSON(ctx, webhookURL, raw)
}

// sendDingTalk posts a markdown message; DingTalk requires the signature as
// URL query params (timestamp + base64-HMAC, URL-encoded) plus the secret
// configured on the robot (加签 mode).
func sendDingTalk(ctx context.Context, webhookURL, secret, title, text string) error {
	payload, err := json.Marshal(map[string]interface{}{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"title": title,
			"text":  text,
		},
	})
	if err != nil {
		return err
	}
	if secret != "" {
		timestamp := time.Now().UnixMilli()
		u, err := url.Parse(webhookURL)
		if err != nil {
			return err
		}
		q := u.Query()
		q.Set("timestamp", strconv.FormatInt(timestamp, 10))
		q.Set("sign", sign(timestamp/1000, secret)) // DingTalk signs in seconds
		u.RawQuery = q.Encode()
		webhookURL = u.String()
	}
	return postJSON(ctx, webhookURL, payload)
}

// sendWeCom posts a markdown message to a WeCom group bot (no signing).
func sendWeCom(ctx context.Context, webhookURL, text string) error {
	payload, err := json.Marshal(map[string]interface{}{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"content": text,
		},
	})
	if err != nil {
		return err
	}
	return postJSON(ctx, webhookURL, payload)
}
