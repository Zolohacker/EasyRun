package protocol

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Zolohacker/EasyRun/internal/faceengine"
)

type Options struct {
	BaseURL     string
	WebBaseURL  string
	Environment int
	Timeout     time.Duration
}

type Client struct {
	baseURL     string
	webBaseURL  string
	environment int
	http        *http.Client
	clock       func() time.Time
	face        faceengine.Analyzer
}

type Device struct {
	ID            string `json:"device_id"`
	ModelName     string `json:"model_name"`
	SystemVersion string `json:"system_version"`
}

type Session struct {
	Token         string `json:"token"`
	SAToken       string `json:"satoken"`
	UserID        string `json:"user_id"`
	UserName      string `json:"user_name,omitempty"`
	StudentNumber string `json:"student_number,omitempty"`
	SchoolCode    string `json:"school_code,omitempty"`
	SchoolName    string `json:"school_name,omitempty"`
}

type Envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type ResponseError struct {
	Code    int
	Message string
}

func (e *ResponseError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("目标服务返回错误 %d", e.Code)
	}
	return fmt.Sprintf("目标服务返回错误 %d：%s", e.Code, e.Message)
}

// IsSessionExpired reports whether an upstream response means that the saved
// login session can no longer be used. 1503 is the code emitted by the current
// client API; the message checks keep the handling compatible with older
// deployments that returned the same condition under a generic response code.
func IsSessionExpired(err error) bool {
	var responseError *ResponseError
	if !errors.As(err, &responseError) {
		return false
	}
	if responseError.Code == 1503 || responseError.Code == http.StatusUnauthorized {
		return true
	}
	message := strings.ToLower(strings.TrimSpace(responseError.Message))
	for _, marker := range []string{
		"登录状态已失效", "登录已失效", "登录失效", "请重新登录", "未登录",
		"token失效", "token已过期", "token过期", "session expired", "login expired",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

type requestOptions struct {
	form      bool
	encrypted bool
	session   Session
}

func NewClient(options Options) (*Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(options.BaseURL), "/")
	if baseURL == "" {
		return nil, errors.New("protocol base URL is required")
	}
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return nil, fmt.Errorf("parse protocol base URL: %w", err)
	}
	if options.Environment == 0 {
		options.Environment = 9
	}
	if options.Timeout <= 0 {
		options.Timeout = 20 * time.Second
	}
	return &Client{
		baseURL: baseURL, webBaseURL: strings.TrimRight(options.WebBaseURL, "/"), environment: options.Environment,
		http: &http.Client{Timeout: options.Timeout}, clock: time.Now,
	}, nil
}

func (c *Client) Environment() int { return c.environment }
func (c *Client) BaseURL() string  { return c.baseURL }

func (c *Client) SetFaceAnalyzer(analyzer faceengine.Analyzer) { c.face = analyzer }

func (c *Client) basePayload(device Device, values map[string]any) map[string]any {
	payload := map[string]any{
		"appVersion": AppVersion, "buildVersion": BuildVersion, "appCode": AppCode,
		"deviceId": device.ID, "platform": Platform, "modelName": device.ModelName,
		"systemVersion": device.SystemVersion, "channel": Channel,
		"timestamp": strconv.FormatInt(c.clock().UnixMilli(), 10),
	}
	for key, value := range values {
		payload[key] = value
	}
	return payload
}

func (c *Client) post(ctx context.Context, endpoint string, device Device, values map[string]any, options requestOptions) (Envelope, error) {
	payload := c.basePayload(device, values)
	body, err := compactJSON(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("encode protocol request: %w", err)
	}
	signature, err := SignBody(body, c.environment)
	if err != nil {
		return Envelope{}, fmt.Errorf("sign protocol request: %w", err)
	}
	cleanEndpoint := strings.TrimLeft(endpoint, "/")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/"+cleanEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return Envelope{}, err
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("Accept-Encoding", "gzip")
	request.Header.Set("User-Agent", userAgent(device))
	request.Header.Set("Authorization", options.session.Token)
	request.Header.Set("satoken", options.session.SAToken)
	request.Header.Set("sign", signature)
	if options.form {
		parts := strings.Split(cleanEndpoint, "/")
		request.Header.Set("app", "run-front")
		if len(parts) > 0 {
			request.Header.Set("api", parts[0])
		}
		if len(parts) > 1 {
			request.Header.Set("v", parts[1])
		}
		request.Header.Set("pv", "2")
		if options.encrypted {
			request.Header.Set("e", "1")
		} else {
			request.Header.Set("e", "0")
		}
		request.Header.Set("k", "")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return Envelope{}, fmt.Errorf("send protocol request: %w", err)
	}
	return decodeEnvelopeResponse(response)
}

// get reproduces the 8.7.9 Retrofit GET path: common parameters are appended
// to the URL query and the request is not given a sign header.
func (c *Client) get(ctx context.Context, endpoint string, device Device, values map[string]any, session Session) (Envelope, error) {
	payload := c.basePayload(device, values)
	cleanEndpoint := strings.TrimLeft(endpoint, "/")
	requestURL, err := url.Parse(c.baseURL + "/" + cleanEndpoint)
	if err != nil {
		return Envelope{}, fmt.Errorf("parse protocol GET URL: %w", err)
	}
	query := requestURL.Query()
	for key, value := range payload {
		query.Set(key, fmt.Sprint(value))
	}
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return Envelope{}, err
	}
	request.Header.Set("Accept-Encoding", "gzip")
	request.Header.Set("User-Agent", userAgent(device))
	request.Header.Set("Authorization", session.Token)
	request.Header.Set("satoken", session.SAToken)
	response, err := c.http.Do(request)
	if err != nil {
		return Envelope{}, fmt.Errorf("send protocol request: %w", err)
	}
	return decodeEnvelopeResponse(response)
}

func decodeEnvelopeResponse(response *http.Response) (Envelope, error) {
	defer response.Body.Close()
	reader := io.Reader(response.Body)
	if strings.EqualFold(response.Header.Get("Content-Encoding"), "gzip") {
		compressed, gzipErr := gzip.NewReader(response.Body)
		if gzipErr != nil {
			return Envelope{}, fmt.Errorf("open gzip protocol response: %w", gzipErr)
		}
		defer compressed.Close()
		reader = compressed
	}
	raw, err := io.ReadAll(io.LimitReader(reader, 16<<20))
	if err != nil {
		return Envelope{}, fmt.Errorf("read protocol response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Envelope{}, fmt.Errorf("protocol HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(raw)))
	}
	var envelope Envelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return Envelope{}, fmt.Errorf("decode protocol response: %w", err)
	}
	if envelope.Code != 0 {
		return envelope, &ResponseError{Code: envelope.Code, Message: envelope.Message}
	}
	return envelope, nil
}

func userAgent(device Device) string {
	brand := device.ModelName
	if before, _, ok := strings.Cut(brand, "|"); ok {
		brand = before
	}
	if brand == "" {
		brand = "Redmi"
	}
	return fmt.Sprintf("ShanDong/%s (%s;Android %s)", AppVersion, strings.ToLower(brand), device.SystemVersion)
}

func decodeData[T any](envelope Envelope) (T, error) {
	var result T
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return result, nil
	}
	if err := json.Unmarshal(envelope.Data, &result); err != nil {
		return result, err
	}
	return result, nil
}
