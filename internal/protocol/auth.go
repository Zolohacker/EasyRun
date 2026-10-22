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
	"strings"
	"unicode/utf8"
)

type captchaData struct {
	Token               string `json:"token"`
	OriginalImageBase64 string `json:"originalImageBase64"`
	JigsawImageBase64   string `json:"jigsawImageBase64"`
	OriginalImageURL    string `json:"originalImageUrl"`
	JigsawImageURL      string `json:"jigsawImageUrl"`
}

type loginResult struct {
	UserID  string `json:"userId"`
	Token   string `json:"token"`
	SAToken string `json:"satoken"`
}

type SMSChallenge struct {
	Ticket      string  `json:"ticket"`
	SolverX     float64 `json:"solver_x"`
	SolverScore float64 `json:"solver_score"`
}

const PasswordVerificationRequiredCode = 10101

// IsPasswordVerificationRequired mirrors PasswordVM in the 8.7.9 client:
// response code 10101 leaves password login pending and moves the user to the
// SMS-code flow.
func IsPasswordVerificationRequired(err error) bool {
	var responseError *ResponseError
	return errors.As(err, &responseError) && responseError.Code == PasswordVerificationRequiredCode
}

func (c *Client) PasswordLogin(ctx context.Context, device Device, loginName, password string) (Session, error) {
	loginName, password = strings.TrimSpace(loginName), strings.TrimSpace(password)
	if loginName == "" || password == "" {
		return Session{}, errors.New("账号和密码均为必填项")
	}
	_, _ = c.post(ctx, "auth/loginCheck", device, map[string]any{"loginName": loginName}, requestOptions{form: true, encrypted: true})
	captchaPoint, _, _, err := c.solveCaptcha(ctx, device, 5)
	if err != nil {
		return Session{}, err
	}
	encryptedPassword, err := EncryptField(password)
	if err != nil {
		return Session{}, err
	}
	envelope, err := c.post(ctx, "auth/loginPassword", device, map[string]any{
		"loginName": loginName, "password": encryptedPassword, "captchaPoint": captchaPoint,
	}, requestOptions{form: true, encrypted: true})
	if err != nil {
		return Session{}, err
	}
	result, err := decodeData[loginResult](envelope)
	if err != nil {
		return Session{}, err
	}
	if result.Token == "" || result.SAToken == "" {
		return Session{}, errors.New("登录响应没有返回完整 token")
	}
	session := Session{Token: result.Token, SAToken: result.SAToken, UserID: result.UserID}
	if err := c.enrichSession(ctx, device, &session); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (c *Client) BeginSMSLogin(ctx context.Context, device Device, loginName string) (SMSChallenge, error) {
	loginName = strings.TrimSpace(loginName)
	if loginName == "" {
		return SMSChallenge{}, errors.New("手机号为必填项")
	}
	terminalType, err := c.loginTerminalType(ctx, device, loginName)
	if err != nil {
		return SMSChallenge{}, err
	}
	captchaPoint, x, score, err := c.solveCaptcha(ctx, device, 5)
	if err != nil {
		return SMSChallenge{}, err
	}
	envelope, err := c.post(ctx, "account/getAuthCode", device, map[string]any{
		"terminalType": terminalType, "sendType": "1", "captchaPoint": captchaPoint,
	}, requestOptions{form: true, encrypted: true})
	if err != nil {
		return SMSChallenge{}, err
	}
	var data struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return SMSChallenge{}, err
	}
	if data.Ticket == "" {
		return SMSChallenge{}, errors.New("验证码接口没有返回 ticket")
	}
	return SMSChallenge{Ticket: data.Ticket, SolverX: x, SolverScore: score}, nil
}

func (c *Client) CompleteSMSLogin(ctx context.Context, device Device, loginName, code, ticket string) (Session, error) {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(ticket) == "" {
		return Session{}, errors.New("短信验证码和 ticket 均为必填项")
	}
	terminalType, err := c.loginTerminalType(ctx, device, loginName)
	if err != nil {
		return Session{}, err
	}
	envelope, err := c.post(ctx, "account/login_v3", device, map[string]any{
		"terminalType": terminalType, "authCode": strings.TrimSpace(code), "ticket": strings.TrimSpace(ticket),
	}, requestOptions{form: true, encrypted: true})
	if err != nil {
		return Session{}, err
	}
	result, err := decodeData[loginResult](envelope)
	if err != nil {
		return Session{}, err
	}
	if result.Token == "" || result.SAToken == "" {
		return Session{}, errors.New("登录响应没有返回完整 token")
	}
	session := Session{Token: result.Token, SAToken: result.SAToken, UserID: result.UserID}
	if err := c.enrichSession(ctx, device, &session); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (c *Client) loginTerminalType(ctx context.Context, device Device, loginName string) (string, error) {
	envelope, err := c.post(ctx, "auth/loginCheck", device, map[string]any{"loginName": strings.TrimSpace(loginName)}, requestOptions{form: true, encrypted: true})
	if err != nil {
		return "", err
	}
	var data struct {
		Phone string `json:"phone"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return "", err
	}
	terminalType := normalizeSensitiveField(data.Phone)
	if terminalType == "" {
		terminalType = strings.TrimSpace(loginName)
	}
	return terminalType, nil
}

func (c *Client) ValidateSession(ctx context.Context, device Device, session Session) (Session, error) {
	envelope, err := c.post(ctx, "account/queryCommonUserInfo", device, nil, requestOptions{form: true, session: session})
	if err != nil {
		return Session{}, err
	}
	mergeSessionFields(envelope.Data, &session)
	if err := c.enrichSchool(ctx, device, &session); IsSessionExpired(err) {
		return Session{}, err
	}
	return session, nil
}

func (c *Client) enrichSession(ctx context.Context, device Device, session *Session) error {
	if envelope, err := c.post(ctx, "account/queryCommonUserInfo", device, nil, requestOptions{form: true, session: *session}); err == nil {
		mergeSessionFields(envelope.Data, session)
	} else if IsSessionExpired(err) {
		return err
	}
	if err := c.enrichSchool(ctx, device, session); IsSessionExpired(err) {
		return err
	}
	return nil
}

func (c *Client) enrichSchool(ctx context.Context, device Device, session *Session) error {
	if envelope, err := c.post(ctx, "api/school/details", device, nil, requestOptions{form: true, session: *session}); err == nil {
		mergeSessionFields(envelope.Data, session)
	} else {
		return err
	}
	return nil
}

func mergeSessionFields(raw json.RawMessage, session *Session) {
	// Older persisted sessions may contain the wire-format ciphertext. Normalize
	// those values before merging the latest profile response.
	session.UserName = normalizeSensitiveField(session.UserName)
	session.StudentNumber = normalizeSensitiveField(session.StudentNumber)
	session.SchoolName = normalizeSensitiveField(session.SchoolName)

	var value any
	if json.Unmarshal(raw, &value) != nil {
		return
	}
	lookup := func(keys ...string) string { return findString(value, keys...) }
	if candidate := lookup("userId", "userCode"); candidate != "" {
		session.UserID = candidate
	}
	if candidate := lookup("userName"); candidate != "" {
		session.UserName = normalizeSensitiveField(candidate)
	} else if session.UserName == "" {
		session.UserName = normalizeSensitiveField(lookup("nickName", "name"))
	}
	if candidate := lookup("studentNumber", "studentNo", "student_number"); candidate != "" {
		session.StudentNumber = normalizeSensitiveField(candidate)
	}
	if candidate := lookup("schoolCode", "school_code"); candidate != "" {
		session.SchoolCode = candidate
	}
	if candidate := lookup("schoolName", "school_name"); candidate != "" {
		session.SchoolName = normalizeSensitiveField(candidate)
	}
}

func normalizeSensitiveField(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	plaintext, err := DecryptField(value)
	if err != nil || !utf8.ValidString(plaintext) {
		return value
	}
	for _, char := range plaintext {
		if char < 0x20 && char != '\t' && char != '\n' && char != '\r' {
			return value
		}
	}
	return strings.TrimSpace(plaintext)
}

func findString(value any, keys ...string) string {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range keys {
			if candidate, exists := typed[key]; exists {
				switch v := candidate.(type) {
				case string:
					if strings.TrimSpace(v) != "" {
						return v
					}
				case float64:
					return fmt.Sprintf("%.0f", v)
				}
			}
		}
		for _, child := range typed {
			if result := findString(child, keys...); result != "" {
				return result
			}
		}
	case []any:
		for _, child := range typed {
			if result := findString(child, keys...); result != "" {
				return result
			}
		}
	}
	return ""
}

func (c *Client) solveCaptcha(ctx context.Context, device Device, attempts int) (string, float64, float64, error) {
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		challenge, err := c.getCaptcha(ctx, device)
		if err != nil {
			lastErr = err
			continue
		}
		background, err := c.captchaImage(ctx, challenge.OriginalImageBase64, challenge.OriginalImageURL)
		if err != nil {
			lastErr = err
			continue
		}
		piece, err := c.captchaImage(ctx, challenge.JigsawImageBase64, challenge.JigsawImageURL)
		if err != nil {
			lastErr = err
			continue
		}
		x, score, err := SolveSliderNotch(background, piece)
		if err != nil {
			lastErr = err
			continue
		}
		point, err := EncryptSliderPoint(x)
		if err != nil {
			lastErr = err
			continue
		}
		passed, err := c.checkCaptcha(ctx, device, challenge.Token, point)
		if err == nil && passed {
			return point, x, score, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = errors.New("滑块校验未通过")
		}
	}
	if lastErr == nil {
		lastErr = errors.New("滑块求解失败")
	}
	return "", 0, 0, fmt.Errorf("自动滑块处理失败：%w", lastErr)
}

func (c *Client) getCaptcha(ctx context.Context, device Device) (captchaData, error) {
	var output captchaData
	envelope, err := c.captchaRequest(ctx, device, "captcha/get", map[string]any{"captchaType": "blockPuzzle"})
	if err != nil {
		return output, err
	}
	return decodeData[captchaData](envelope)
}

func (c *Client) checkCaptcha(ctx context.Context, device Device, token, encryptedPoint string) (bool, error) {
	envelope, err := c.captchaRequest(ctx, device, "captcha/check", map[string]any{
		"captchaType": "blockPuzzle", "pointJson": encryptedPoint, "token": token,
	})
	if err != nil {
		return false, err
	}
	var data struct {
		Result bool `json:"result"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return false, err
	}
	return data.Result, nil
}

func (c *Client) captchaRequest(ctx context.Context, device Device, endpoint string, payload any) (Envelope, error) {
	raw, err := compactJSON(payload)
	if err != nil {
		return Envelope{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/"+endpoint, strings.NewReader(string(raw)))
	if err != nil {
		return Envelope{}, err
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("Origin", c.webBaseURL)
	request.Header.Set("Referer", c.webBaseURL+"/")
	request.Header.Set("User-Agent", userAgent(device))
	response, err := c.http.Do(request)
	if err != nil {
		return Envelope{}, err
	}
	defer response.Body.Close()
	reader := io.Reader(response.Body)
	if strings.EqualFold(response.Header.Get("Content-Encoding"), "gzip") {
		compressed, gzipErr := gzip.NewReader(response.Body)
		if gzipErr != nil {
			return Envelope{}, gzipErr
		}
		defer compressed.Close()
		reader = compressed
	}
	body, err := io.ReadAll(io.LimitReader(reader, 8<<20))
	if err != nil {
		return Envelope{}, err
	}
	var envelope Envelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return Envelope{}, err
	}
	if envelope.Code != 0 {
		return envelope, &ResponseError{Code: envelope.Code, Message: envelope.Message}
	}
	return envelope, nil
}

func (c *Client) captchaImage(ctx context.Context, encoded, location string) ([]byte, error) {
	if encoded != "" {
		return decodeBase64Image(encoded)
	}
	if location == "" {
		return nil, errors.New("验证码响应没有图片")
	}
	parsed, err := url.Parse(location)
	if err != nil {
		return nil, err
	}
	if !parsed.IsAbs() {
		base, _ := url.Parse(c.webBaseURL + "/")
		parsed = base.ResolveReference(parsed)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("captcha image HTTP %d", response.StatusCode)
	}
	reader := io.Reader(response.Body)
	if strings.EqualFold(response.Header.Get("Content-Encoding"), "gzip") {
		compressed, gzipErr := gzip.NewReader(response.Body)
		if gzipErr != nil {
			return nil, gzipErr
		}
		defer compressed.Close()
		reader = compressed
	}
	return io.ReadAll(io.LimitReader(reader, 4<<20))
}
