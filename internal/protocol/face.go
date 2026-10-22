package protocol

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
)

const maxFaceImageBytes = 6 << 20

// UserFaceConfig mirrors run-front/face/getUserFaceImg in the 8.7.9 client.
// FaceImg is kept inside the protocol/runner layers and is never returned by
// the local account API.
type UserFaceConfig struct {
	FaceEnabled     int    `json:"faceEnabled"`
	FaceImg         string `json:"faceImg"`
	SimilarityValue int    `json:"similarityValue"`
	FaceAuth        bool   `json:"faceAuth"`
	RunRecordCode   string `json:"runRecordCode"`
	FaceTime        int    `json:"faceTime"`
	LeftTime        int    `json:"leftTime"`
	BodyEnable      bool   `json:"bodyEnable"`
	BodyRate        int    `json:"bodyRate"`
	FaceModelType   int    `json:"faceModelType"`
	FaceCheckIndex  int    `json:"faceCheckIndex"`
}

type OSSToken struct {
	SecurityToken   string `json:"securityToken"`
	AccessKeySecret string `json:"accessKeySecret"`
	AccessKeyID     string `json:"accessKeyId"`
	Expiration      int64  `json:"expiration"`
	Domain          string `json:"domain"`
	Directory       string `json:"directory"`
}

type FaceCompareResult struct {
	RequestID  string  `json:"request_id"`
	Confidence float64 `json:"confidence"`
}

// FaceDetectionResult is the sanitized result returned by an explicit
// end-to-end face check. Remote image URLs and temporary OSS credentials are
// deliberately excluded from the response.
type FaceDetectionResult struct {
	Source     string    `json:"source"`
	Provider   string    `json:"provider"`
	RequestID  string    `json:"request_id"`
	Confidence float64   `json:"confidence"`
	Threshold  int       `json:"threshold"`
	Passed     bool      `json:"passed"`
	CheckedAt  time.Time `json:"checked_at"`
	ElapsedMS  int64     `json:"elapsed_ms"`
}

type FaceInput struct {
	Image                []byte
	ContentType          string
	Source               string
	BaseURL              string
	ReferenceImage       []byte
	ReferenceContentType string
}

type FaceCheckConfig struct {
	LowerLimit int `json:"lowerLimit"`
	UpperLimit int `json:"upperLimit"`
}

type CheckRunConfig struct {
	RunScene            int               `json:"runScene"`
	FaceAuth            bool              `json:"faceAuth"`
	FaceImg             string            `json:"faceImg"`
	SimilarityValue     int               `json:"similarityValue"`
	FaceTime            int               `json:"faceTime"`
	BodyEnable          bool              `json:"bodyEnable"`
	BodyRate            int               `json:"bodyRate"`
	FaceModelType       int               `json:"faceModelType"`
	FaceCheckConfigList []FaceCheckConfig `json:"faceCheckConfigList"`
	FaceEnabled         int               `json:"faceEnabled"`
}

type FaceCheckRecord struct {
	CheckIndex         int      `json:"checkIndex"`
	RandomDistance     int      `json:"randomDistance"`
	HasChecked         bool     `json:"hasChecked"`
	FinishFaceCheck    bool     `json:"finishFaceCheck"`
	PopTime            *int64   `json:"popTime,omitempty"`
	CheckDistance      *int64   `json:"checkDistance,omitempty"`
	CheckLng           *float64 `json:"checkLng,omitempty"`
	CheckLat           *float64 `json:"checkLat,omitempty"`
	RunFaceUpload      bool     `json:"runFaceUpload"`
	RunFaceImg         string   `json:"runFaceImg,omitempty"`
	Confidence         *float64 `json:"confidence,omitempty"`
	Rate               *float64 `json:"rate,omitempty"`
	Label              string   `json:"label,omitempty"`
	FaceRequestID      string   `json:"faceRequestId,omitempty"`
	BodyRequestID      string   `json:"bodyRequestId,omitempty"`
	FaceCheckErrorCode string   `json:"faceCheckErrorCode,omitempty"`
	BodyCheckErrorCode string   `json:"bodyCheckErrorCode,omitempty"`
	ExtendParam        string   `json:"extendParam,omitempty"`
}

func (c *Client) UserFaceConfig(ctx context.Context, device Device, session Session) (UserFaceConfig, error) {
	envelope, err := c.post(ctx, "face/getUserFaceImg", device, nil, requestOptions{session: session})
	if err != nil {
		return UserFaceConfig{}, err
	}
	result, err := decodeData[UserFaceConfig](envelope)
	if err != nil {
		return UserFaceConfig{}, fmt.Errorf("解析账号人脸配置: %w", err)
	}
	if result.SimilarityValue <= 0 {
		result.SimilarityValue = 50
	}
	if result.FaceModelType <= 0 {
		result.FaceModelType = 1
	}
	return result, nil
}

// DownloadFaceImage follows the app's secure-cache source: the upstream face
// URL is downloaded once and the runner encrypts the bytes before persistence.
func (c *Client) DownloadFaceImage(ctx context.Context, device Device, rawURL string) ([]byte, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, "", errors.New("账号人脸底图地址无效")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("User-Agent", userAgent(device))
	response, err := c.http.Do(request)
	if err != nil {
		return nil, "", fmt.Errorf("下载账号人脸底图: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("下载账号人脸底图: HTTP %d", response.StatusCode)
	}
	reader := io.LimitReader(response.Body, maxFaceImageBytes+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", fmt.Errorf("读取账号人脸底图: %w", err)
	}
	if len(data) == 0 || len(data) > maxFaceImageBytes {
		return nil, "", errors.New("账号人脸底图为空或超过 6 MB")
	}
	contentType := normalizeImageMIME(response.Header.Get("Content-Type"), data)
	if contentType == "" {
		return nil, "", errors.New("账号人脸底图不是支持的图片格式")
	}
	return data, contentType, nil
}

func normalizeImageMIME(header string, data []byte) string {
	value, _, _ := mime.ParseMediaType(header)
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "image/jpg" {
		value = "image/jpeg"
	}
	if value != "image/jpeg" && value != "image/png" && value != "image/webp" {
		value = strings.ToLower(http.DetectContentType(data))
	}
	switch value {
	case "image/jpeg", "image/png", "image/webp":
		return value
	default:
		return ""
	}
}

func (c *Client) ossToken(ctx context.Context, device Device, session Session, tokenType string) (OSSToken, error) {
	envelope, err := c.post(ctx, "aliyun/oss/getToken", device, map[string]any{"tokenType": tokenType}, requestOptions{form: true, session: session})
	if err != nil {
		return OSSToken{}, err
	}
	token, err := decodeData[OSSToken](envelope)
	if err != nil {
		return OSSToken{}, fmt.Errorf("解析阿里云临时凭据: %w", err)
	}
	if token.AccessKeyID == "" || token.AccessKeySecret == "" || token.SecurityToken == "" {
		return OSSToken{}, errors.New("阿里云临时凭据不完整")
	}
	return token, nil
}

// UploadRunFace reproduces UploadFileType.UPLOAD_FACE_RUN (face/run) and the
// Android OSS STS upload. The object name is the image MD5 plus its extension.
func (c *Client) UploadRunFace(ctx context.Context, device Device, session Session, data []byte, contentType string) (string, error) {
	contentType = normalizeImageMIME(contentType, data)
	if contentType == "" || len(data) == 0 || len(data) > maxFaceImageBytes {
		return "", errors.New("待上传人脸图片为空、过大或格式不受支持")
	}
	token, err := c.ossToken(ctx, device, session, "")
	if err != nil {
		return "", fmt.Errorf("获取人脸上传凭据: %w", err)
	}
	extension := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}[contentType]
	digest := md5.Sum(data)
	fileName := hex.EncodeToString(digest[:]) + extension
	directory := strings.Trim(token.Directory, "/")
	objectName := path.Join(directory, "face/run", fileName)
	const bucket = "sd-campus-badge"
	const endpoint = "oss-cn-hangzhou.aliyuncs.com"
	requestURL := (&url.URL{Scheme: "https", Host: bucket + "." + endpoint, Path: "/" + objectName}).String()
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, requestURL, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	date := c.clock().UTC().Format(http.TimeFormat)
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Date", date)
	request.Header.Set("x-oss-security-token", token.SecurityToken)
	canonicalHeaders := "x-oss-security-token:" + token.SecurityToken + "\n"
	canonicalResource := "/" + bucket + "/" + objectName
	stringToSign := http.MethodPut + "\n\n" + contentType + "\n" + date + "\n" + canonicalHeaders + canonicalResource
	mac := hmac.New(sha1.New, []byte(token.AccessKeySecret))
	_, _ = mac.Write([]byte(stringToSign))
	request.Header.Set("Authorization", "OSS "+token.AccessKeyID+":"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	response, err := c.http.Do(request)
	if err != nil {
		return "", fmt.Errorf("上传跑步人脸: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return "", fmt.Errorf("上传跑步人脸: HTTP %d %s", response.StatusCode, strings.TrimSpace(string(raw)))
	}
	domain := strings.TrimRight(strings.TrimSpace(token.Domain), "/")
	if domain == "" {
		domain = "https://" + bucket + "." + endpoint
	} else if !strings.HasPrefix(domain, "http://") && !strings.HasPrefix(domain, "https://") {
		domain = "https://" + domain
	}
	return domain + "/" + objectName, nil
}

// CompareFace calls the same Facebody 2019-12-30 API used by the Android
// client. Supplying URLs avoids the SDK's temporary OpenPlatform upload path.
func (c *Client) CompareFace(ctx context.Context, device Device, session Session, imageURLA, imageURLB string) (FaceCompareResult, error) {
	if strings.TrimSpace(imageURLA) == "" || strings.TrimSpace(imageURLB) == "" {
		return FaceCompareResult{}, errors.New("人脸比对缺少底图或本次识别图片")
	}
	token, err := c.ossToken(ctx, device, session, "1")
	if err != nil {
		return FaceCompareResult{}, fmt.Errorf("获取人脸识别凭据: %w", err)
	}
	parameters := map[string]string{
		"AccessKeyId": token.AccessKeyID, "Action": "CompareFace", "Format": "JSON",
		"RegionId": "cn-shanghai", "SignatureMethod": "HMAC-SHA1",
		"SignatureNonce":   fmt.Sprintf("%d-%x", c.clock().UnixNano(), md5.Sum([]byte(imageURLA+imageURLB))),
		"SignatureVersion": "1.0", "Timestamp": c.clock().UTC().Format("2006-01-02T15:04:05Z"),
		"Version": "2019-12-30", "SecurityToken": token.SecurityToken,
		"ImageURLA": imageURLA, "ImageURLB": imageURLB,
	}
	parameters["Signature"] = rpcSignature(http.MethodPost, parameters, token.AccessKeySecret)
	form := url.Values{}
	for key, value := range parameters {
		form.Set(key, value)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://facebody.cn-shanghai.aliyuncs.com/", strings.NewReader(form.Encode()))
	if err != nil {
		return FaceCompareResult{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.http.Do(request)
	if err != nil {
		return FaceCompareResult{}, fmt.Errorf("执行人脸比对: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return FaceCompareResult{}, fmt.Errorf("读取人脸比对结果: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return FaceCompareResult{}, fmt.Errorf("执行人脸比对: HTTP %d %s", response.StatusCode, strings.TrimSpace(string(raw)))
	}
	var payload struct {
		RequestID string `json:"RequestId"`
		Data      struct {
			Confidence float64 `json:"Confidence"`
		} `json:"Data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return FaceCompareResult{}, fmt.Errorf("解析人脸比对结果: %w", err)
	}
	if payload.RequestID == "" {
		return FaceCompareResult{}, errors.New("人脸比对响应缺少 RequestId")
	}
	return FaceCompareResult{RequestID: payload.RequestID, Confidence: payload.Data.Confidence}, nil
}

// DetectFace executes the complete active-check path used by the running
// client: upload the selected local image to face/run, then compare it against
// the account's bound reference image through Aliyun Facebody CompareFace.
func (c *Client) DetectFace(ctx context.Context, device Device, session Session, input FaceInput, baseURL string, threshold int) (FaceDetectionResult, error) {
	if len(input.Image) == 0 {
		return FaceDetectionResult{}, errors.New("主动人脸检测缺少待检测图片")
	}
	if strings.TrimSpace(baseURL) == "" {
		return FaceDetectionResult{}, errors.New("主动人脸检测缺少账号绑定底图")
	}
	if threshold <= 0 {
		threshold = 50
	}
	started := time.Now()
	probeURL, err := c.UploadRunFace(ctx, device, session, input.Image, input.ContentType)
	if err != nil {
		return FaceDetectionResult{}, err
	}
	comparison, err := c.CompareFace(ctx, device, session, probeURL, baseURL)
	if err != nil {
		return FaceDetectionResult{}, err
	}
	return FaceDetectionResult{
		Source: strings.TrimSpace(input.Source), Provider: "app-sts/aliyun-facebody",
		RequestID: comparison.RequestID, Confidence: comparison.Confidence,
		Threshold: threshold, Passed: comparison.Confidence >= float64(threshold),
		CheckedAt: c.clock().UTC(), ElapsedMS: time.Since(started).Milliseconds(),
	}, nil
}

// DetectLocalFace runs the same detector/alignment/embedding/comparison model
// family as the app's faceModelType=2 branch. It performs no cloud comparison
// request; the resource pack's nonlinear converter produces the 0..100 score.
func (c *Client) DetectLocalFace(ctx context.Context, input FaceInput, threshold int) (FaceDetectionResult, error) {
	if c.face == nil {
		return FaceDetectionResult{}, errors.New("本地人脸引擎尚未配置")
	}
	if len(input.Image) == 0 {
		return FaceDetectionResult{}, errors.New("本地人脸检测缺少待检测图片")
	}
	reference := input.ReferenceImage
	if len(reference) == 0 {
		reference = input.Image
	}
	if threshold <= 0 {
		threshold = 50
	}
	started := time.Now()
	comparison, err := c.face.Compare(ctx, reference, input.Image)
	if err != nil {
		return FaceDetectionResult{}, fmt.Errorf("执行本地人脸模型: %w", err)
	}
	return FaceDetectionResult{
		Source: strings.TrimSpace(input.Source), Provider: c.face.Name(),
		Confidence: comparison.Confidence, Threshold: threshold,
		Passed: comparison.Confidence >= float64(threshold), CheckedAt: c.clock().UTC(),
		ElapsedMS: time.Since(started).Milliseconds(),
	}, nil
}

func rpcSignature(method string, parameters map[string]string, secret string) string {
	keys := make([]string, 0, len(parameters))
	for key := range parameters {
		if key != "Signature" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, percentEncode(key)+"="+percentEncode(parameters[key]))
	}
	canonical := strings.Join(parts, "&")
	stringToSign := strings.ToUpper(method) + "&%2F&" + percentEncode(canonical)
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	_, _ = mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func percentEncode(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(url.QueryEscape(value), "+", "%20"), "*", "%2A"), "%7E", "~")
}
