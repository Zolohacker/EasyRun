package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strings"
	"time"

	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/Zolohacker/EasyRun/internal/protocol"
)

const maxStoredFaceBytes = 6 << 20

func (s *Service) FaceProfile(ctx context.Context, accountID string) (platform.FaceProfile, error) {
	account, err := s.store.GetAccount(ctx, accountID)
	if err != nil {
		return platform.FaceProfile{}, err
	}
	return account.FaceProfile(), nil
}

func (s *Service) SyncFaceProfile(ctx context.Context, accountID string) (platform.FaceProfile, error) {
	account, session, unlock, err := s.authenticated(ctx, accountID)
	if err != nil {
		return platform.FaceProfile{}, err
	}
	defer unlock()
	return s.syncFaceProfile(ctx, &account, session)
}

func (s *Service) syncFaceProfile(ctx context.Context, account *platform.Account, session protocol.Session) (platform.FaceProfile, error) {
	config, err := s.client.UserFaceConfig(ctx, accountDevice(*account), session)
	if err != nil {
		s.observeSessionError(ctx, account, err)
		return platform.FaceProfile{}, fmt.Errorf("读取账号人脸配置: %w", err)
	}
	var baseCipher []byte
	baseMIME := ""
	if strings.TrimSpace(config.FaceImg) != "" {
		data, contentType, downloadErr := s.client.DownloadFaceImage(ctx, accountDevice(*account), config.FaceImg)
		if downloadErr != nil {
			return platform.FaceProfile{}, downloadErr
		}
		baseCipher, err = s.vault.Encrypt(data, faceBaseAAD(account.ID))
		if err != nil {
			return platform.FaceProfile{}, fmt.Errorf("加密账号人脸底图: %w", err)
		}
		baseMIME = contentType
	}
	now := time.Now().UTC()
	account.FaceEnabled = config.FaceEnabled
	account.FaceAuth = config.FaceAuth
	account.FaceSimilarityValue = config.SimilarityValue
	account.FaceTime = config.FaceTime
	account.FaceBodyEnabled = config.BodyEnable
	account.FaceBodyRate = config.BodyRate
	account.FaceModelType = config.FaceModelType
	account.FaceBaseURL = strings.TrimSpace(config.FaceImg)
	account.FaceBaseCipher = baseCipher
	account.FaceBaseMIME = baseMIME
	account.FaceSyncedAt = &now
	updated, err := s.store.UpdateAccount(ctx, *account)
	if err != nil {
		return platform.FaceProfile{}, err
	}
	*account = updated
	return updated.FaceProfile(), nil
}

func (s *Service) SetCustomFace(ctx context.Context, accountID, contentType string, data []byte) (platform.FaceProfile, error) {
	if len(data) == 0 || len(data) > maxStoredFaceBytes {
		return platform.FaceProfile{}, errors.New("人脸图片为空或超过 6 MB")
	}
	contentType, err := validateFaceImage(contentType, data)
	if err != nil {
		return platform.FaceProfile{}, err
	}
	unlock := s.lock(accountID)
	defer unlock()
	account, err := s.store.GetAccount(ctx, accountID)
	if err != nil {
		return platform.FaceProfile{}, err
	}
	ciphertext, err := s.vault.Encrypt(data, faceCustomAAD(account.ID))
	if err != nil {
		return platform.FaceProfile{}, fmt.Errorf("加密自定义人脸: %w", err)
	}
	account.FaceCustomCipher = ciphertext
	account.FaceCustomMIME = contentType
	updated, err := s.store.UpdateAccount(ctx, account)
	if err != nil {
		return platform.FaceProfile{}, err
	}
	return updated.FaceProfile(), nil
}

func (s *Service) ClearCustomFace(ctx context.Context, accountID string) (platform.FaceProfile, error) {
	unlock := s.lock(accountID)
	defer unlock()
	account, err := s.store.GetAccount(ctx, accountID)
	if err != nil {
		return platform.FaceProfile{}, err
	}
	account.FaceCustomCipher = nil
	account.FaceCustomMIME = ""
	updated, err := s.store.UpdateAccount(ctx, account)
	if err != nil {
		return platform.FaceProfile{}, err
	}
	return updated.FaceProfile(), nil
}

func (s *Service) FaceImage(ctx context.Context, accountID, requestedSource string) ([]byte, string, string, error) {
	account, err := s.store.GetAccount(ctx, accountID)
	if err != nil {
		return nil, "", "", err
	}
	source := strings.ToLower(strings.TrimSpace(requestedSource))
	if source == "" || source == "effective" {
		if len(account.FaceCustomCipher) > 0 {
			source = "custom"
		} else {
			source = "base"
		}
	}
	var ciphertext []byte
	var contentType, aad string
	switch source {
	case "base":
		ciphertext, contentType, aad = account.FaceBaseCipher, account.FaceBaseMIME, faceBaseAAD(account.ID)
	case "custom":
		ciphertext, contentType, aad = account.FaceCustomCipher, account.FaceCustomMIME, faceCustomAAD(account.ID)
	default:
		return nil, "", "", errors.New("人脸图片来源只支持 base、custom 或 effective")
	}
	if len(ciphertext) == 0 {
		return nil, "", source, errors.New("该账号没有对应的人脸图片")
	}
	data, err := s.vault.Decrypt(ciphertext, aad)
	if err != nil {
		return nil, "", source, fmt.Errorf("解密账号人脸图片: %w", err)
	}
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	return data, contentType, source, nil
}

// DetectFace performs an explicit face check without creating a run record.
// The account configuration is refreshed first. Cloud-model accounts use the
// remote comparison path; faceModelType=2 is evaluated by the configured local
// MNN model pack.
func (s *Service) DetectFace(ctx context.Context, accountID, requestedSource string) (protocol.FaceDetectionResult, error) {
	account, session, unlock, err := s.authenticated(ctx, accountID)
	if err != nil {
		return protocol.FaceDetectionResult{}, err
	}
	defer unlock()

	// An explicit check always reads the latest model selector from the app API.
	if _, err := s.syncFaceProfile(ctx, &account, session); err != nil {
		return protocol.FaceDetectionResult{}, err
	}
	input, err := s.faceInput(account, requestedSource)
	if err != nil {
		return protocol.FaceDetectionResult{}, err
	}
	if input == nil {
		return protocol.FaceDetectionResult{}, errors.New("该账号没有可用于主动检测的人脸图片")
	}
	if account.FaceModelType == 2 {
		return s.client.DetectLocalFace(ctx, *input, account.FaceSimilarityValue)
	}
	result, err := s.client.DetectFace(ctx, accountDevice(account), session, *input, account.FaceBaseURL, account.FaceSimilarityValue)
	if err != nil {
		s.observeSessionError(ctx, &account, err)
	}
	return result, err
}

func (s *Service) effectiveFaceInput(account platform.Account) (*protocol.FaceInput, error) {
	return s.faceInput(account, "effective")
}

func (s *Service) faceInput(account platform.Account, requestedSource string) (*protocol.FaceInput, error) {
	var ciphertext []byte
	var contentType, source, aad string
	switch strings.ToLower(strings.TrimSpace(requestedSource)) {
	case "", "effective":
		if len(account.FaceCustomCipher) > 0 {
			ciphertext, contentType, source, aad = account.FaceCustomCipher, account.FaceCustomMIME, "custom", faceCustomAAD(account.ID)
		} else if len(account.FaceBaseCipher) > 0 {
			ciphertext, contentType, source, aad = account.FaceBaseCipher, account.FaceBaseMIME, "base", faceBaseAAD(account.ID)
		}
	case "custom":
		ciphertext, contentType, source, aad = account.FaceCustomCipher, account.FaceCustomMIME, "custom", faceCustomAAD(account.ID)
	case "base":
		ciphertext, contentType, source, aad = account.FaceBaseCipher, account.FaceBaseMIME, "base", faceBaseAAD(account.ID)
	default:
		return nil, errors.New("主动检测图片来源只支持 base、custom 或 effective")
	}
	if len(ciphertext) == 0 {
		if source == "custom" {
			return nil, errors.New("该账号尚未设置自定义人脸")
		}
		if source == "base" {
			return nil, errors.New("该账号尚未同步绑定人脸底图")
		}
		return nil, nil
	}
	data, err := s.vault.Decrypt(ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("解密跑步人脸图片: %w", err)
	}
	reference := data
	referenceMIME := contentType
	if source != "base" && len(account.FaceBaseCipher) > 0 {
		reference, err = s.vault.Decrypt(account.FaceBaseCipher, faceBaseAAD(account.ID))
		if err != nil {
			return nil, fmt.Errorf("解密账号人脸底图: %w", err)
		}
		referenceMIME = account.FaceBaseMIME
	}
	return &protocol.FaceInput{
		Image: data, ContentType: contentType, Source: source, BaseURL: account.FaceBaseURL,
		ReferenceImage: reference, ReferenceContentType: referenceMIME,
	}, nil
}

func validateFaceImage(contentType string, data []byte) (string, error) {
	detected := strings.ToLower(http.DetectContentType(data))
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if contentType == "image/jpg" {
		contentType = "image/jpeg"
	}
	if detected != "image/jpeg" && detected != "image/png" {
		return "", errors.New("人脸图片只支持 JPG 或 PNG")
	}
	if contentType != "" && contentType != detected && !(contentType == "image/jpeg" && detected == "image/jpeg") {
		return "", errors.New("人脸图片格式与文件内容不一致")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", errors.New("人脸图片已损坏或格式不受支持")
	}
	if config.Width < 120 || config.Height < 120 {
		return "", errors.New("人脸图片尺寸至少为 120×120")
	}
	if config.Width > 8192 || config.Height > 8192 || int64(config.Width)*int64(config.Height) > 32_000_000 {
		return "", errors.New("人脸图片尺寸过大")
	}
	return detected, nil
}

func faceBaseAAD(accountID string) string   { return "account:" + accountID + ":face-base" }
func faceCustomAAD(accountID string) string { return "account:" + accountID + ":face-custom" }
