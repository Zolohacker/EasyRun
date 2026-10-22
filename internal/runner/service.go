package runner

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/Zolohacker/EasyRun/internal/activity"
	"github.com/Zolohacker/EasyRun/internal/geo"
	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/Zolohacker/EasyRun/internal/protocol"
	"github.com/Zolohacker/EasyRun/internal/route"
	"github.com/Zolohacker/EasyRun/internal/securestore"
	"github.com/Zolohacker/EasyRun/internal/store"
)

type Service struct {
	store     store.Store
	vault     *securestore.Vault
	client    *protocol.Client
	planner   RoutePlanner
	generator *activity.Generator
	locks     sync.Map
}

type RoutePlanner interface {
	PlanRoute(context.Context, route.PlanRequest) (route.Route, error)
}

type Resources struct {
	Plans         []platform.RunningPlan `json:"plans"`
	Areas         []platform.RunningArea `json:"areas"`
	Face          platform.FaceProfile   `json:"face"`
	FaceSyncError string                 `json:"face_sync_error,omitempty"`
}

// AccountLoginResult is returned by both first-time onboarding and re-login.
// Account is present after a complete login. Draft and Challenge are present
// when the upstream password flow requires an SMS verification step.
type AccountLoginResult struct {
	Account              *platform.Account      `json:"account,omitempty"`
	Draft                *platform.Account      `json:"draft,omitempty"`
	VerificationRequired bool                   `json:"verification_required"`
	Challenge            *protocol.SMSChallenge `json:"challenge,omitempty"`
}

type RecordInspection struct {
	Record     json.RawMessage                 `json:"record"`
	Inspection activity.RemoteRecordInspection `json:"inspection"`
}

func New(database store.Store, vault *securestore.Vault, client *protocol.Client, planner RoutePlanner) *Service {
	return &Service{store: database, vault: vault, client: client, planner: planner, generator: activity.NewGenerator()}
}

func (s *Service) UpdateAccount(ctx context.Context, desired platform.Account, password string) (platform.Account, error) {
	existing, err := s.store.GetAccount(ctx, desired.ID)
	if err != nil {
		return platform.Account{}, err
	}
	desired.PasswordCipher = existing.PasswordCipher
	desired.SessionCipher = existing.SessionCipher
	desired.FaceEnabled = existing.FaceEnabled
	desired.FaceAuth = existing.FaceAuth
	desired.FaceSimilarityValue = existing.FaceSimilarityValue
	desired.FaceTime = existing.FaceTime
	desired.FaceBodyEnabled = existing.FaceBodyEnabled
	desired.FaceBodyRate = existing.FaceBodyRate
	desired.FaceModelType = existing.FaceModelType
	desired.FaceBaseURL = existing.FaceBaseURL
	desired.FaceBaseCipher = existing.FaceBaseCipher
	desired.FaceBaseMIME = existing.FaceBaseMIME
	desired.FaceCustomCipher = existing.FaceCustomCipher
	desired.FaceCustomMIME = existing.FaceCustomMIME
	desired.FaceSyncedAt = existing.FaceSyncedAt
	desired.StudentNumber = existing.StudentNumber
	desired.SchoolCode = existing.SchoolCode
	desired.SchoolName = existing.SchoolName
	desired.AuthStatus = existing.AuthStatus
	desired.AuthMessage = existing.AuthMessage
	desired.LastAuthAt = existing.LastAuthAt
	if strings.TrimSpace(password) != "" {
		desired.PasswordCipher, err = s.vault.Encrypt([]byte(password), passwordAAD(desired.ID))
		if err != nil {
			return platform.Account{}, err
		}
		// A password change invalidates the old remote session.
		desired.SessionCipher = nil
		desired.AuthStatus = "configured"
		desired.AuthMessage = "密码已更新，请重新登录"
		desired.LastAuthAt = nil
	}
	s.applyDefaults(&desired)
	return s.store.UpdateAccount(ctx, desired)
}

func (s *Service) PasswordLogin(ctx context.Context, accountID string) (AccountLoginResult, error) {
	unlock := s.lock(accountID)
	defer unlock()
	account, err := s.store.GetAccount(ctx, accountID)
	if err != nil {
		return AccountLoginResult{}, err
	}
	password, err := s.vault.Decrypt(account.PasswordCipher, passwordAAD(account.ID))
	if err != nil {
		return AccountLoginResult{}, err
	}
	if len(password) == 0 {
		return AccountLoginResult{}, errors.New("该账号尚未保存密码")
	}
	session, err := s.client.PasswordLogin(ctx, accountDevice(account), account.LoginName, string(password))
	if err != nil {
		if protocol.IsPasswordVerificationRequired(err) {
			challenge, challengeErr := s.client.BeginSMSLogin(ctx, accountDevice(account), account.LoginName)
			if challengeErr != nil {
				s.recordAuthError(ctx, &account, challengeErr)
				return AccountLoginResult{}, challengeErr
			}
			account.AuthStatus = "sms_pending"
			account.AuthMessage = "密码校验后需要短信验证码"
			updated, updateErr := s.store.UpdateAccount(ctx, account)
			if updateErr != nil {
				return AccountLoginResult{}, updateErr
			}
			return AccountLoginResult{Account: &updated, VerificationRequired: true, Challenge: &challenge}, nil
		}
		s.recordAuthError(ctx, &account, err)
		return AccountLoginResult{}, err
	}
	account, err = s.persistSession(ctx, account, session)
	if err != nil {
		return AccountLoginResult{}, err
	}
	_, _ = s.syncResources(ctx, &account, session)
	account, err = s.store.GetAccount(ctx, account.ID)
	if err != nil {
		return AccountLoginResult{}, err
	}
	return AccountLoginResult{Account: &account}, nil
}

// OnboardPassword authenticates against the target service before creating a
// local row. A 10101 response starts the same SMS continuation used by the
// Android client and returns the generated device draft without persisting it.
func (s *Service) OnboardPassword(ctx context.Context, account platform.Account, password string) (AccountLoginResult, error) {
	account = s.prepareOnboardingAccount(account)
	if strings.TrimSpace(password) == "" {
		return AccountLoginResult{}, errors.New("密码登录需要填写账号密码")
	}
	if err := s.ensureLoginAvailable(ctx, account.LoginName); err != nil {
		return AccountLoginResult{}, err
	}
	session, err := s.client.PasswordLogin(ctx, accountDevice(account), account.LoginName, password)
	if err != nil {
		if protocol.IsPasswordVerificationRequired(err) {
			challenge, challengeErr := s.client.BeginSMSLogin(ctx, accountDevice(account), account.LoginName)
			if challengeErr != nil {
				return AccountLoginResult{}, challengeErr
			}
			return AccountLoginResult{Draft: &account, VerificationRequired: true, Challenge: &challenge}, nil
		}
		return AccountLoginResult{}, err
	}
	created, err := s.createAuthenticatedAccount(ctx, account, password, session)
	if err != nil {
		return AccountLoginResult{}, err
	}
	return AccountLoginResult{Account: &created}, nil
}

// BeginOnboardSMS requests a verification code without creating a database
// row. The returned draft carries the generated device id into verification.
func (s *Service) BeginOnboardSMS(ctx context.Context, account platform.Account) (AccountLoginResult, error) {
	account = s.prepareOnboardingAccount(account)
	if err := s.ensureLoginAvailable(ctx, account.LoginName); err != nil {
		return AccountLoginResult{}, err
	}
	challenge, err := s.client.BeginSMSLogin(ctx, accountDevice(account), account.LoginName)
	if err != nil {
		return AccountLoginResult{}, err
	}
	return AccountLoginResult{Draft: &account, VerificationRequired: true, Challenge: &challenge}, nil
}

// CompleteOnboardSMS performs the remote login first and inserts the account
// only after a complete token pair has been received.
func (s *Service) CompleteOnboardSMS(ctx context.Context, account platform.Account, password, code, ticket string) (platform.Account, error) {
	account = s.prepareOnboardingAccount(account)
	if err := s.ensureLoginAvailable(ctx, account.LoginName); err != nil {
		return platform.Account{}, err
	}
	session, err := s.client.CompleteSMSLogin(ctx, accountDevice(account), account.LoginName, code, ticket)
	if err != nil {
		return platform.Account{}, err
	}
	return s.createAuthenticatedAccount(ctx, account, password, session)
}

func (s *Service) BeginSMS(ctx context.Context, accountID string) (protocol.SMSChallenge, error) {
	unlock := s.lock(accountID)
	defer unlock()
	account, err := s.store.GetAccount(ctx, accountID)
	if err != nil {
		return protocol.SMSChallenge{}, err
	}
	challenge, err := s.client.BeginSMSLogin(ctx, accountDevice(account), account.LoginName)
	if err != nil {
		s.recordAuthError(ctx, &account, err)
		return protocol.SMSChallenge{}, err
	}
	account.AuthStatus = "sms_pending"
	account.AuthMessage = "短信验证码已发送"
	_, _ = s.store.UpdateAccount(ctx, account)
	return challenge, nil
}

func (s *Service) CompleteSMS(ctx context.Context, accountID, code, ticket string) (platform.Account, error) {
	unlock := s.lock(accountID)
	defer unlock()
	account, err := s.store.GetAccount(ctx, accountID)
	if err != nil {
		return platform.Account{}, err
	}
	session, err := s.client.CompleteSMSLogin(ctx, accountDevice(account), account.LoginName, code, ticket)
	if err != nil {
		s.recordAuthError(ctx, &account, err)
		return platform.Account{}, err
	}
	account, err = s.persistSession(ctx, account, session)
	if err != nil {
		return platform.Account{}, err
	}
	_, _ = s.syncResources(ctx, &account, session)
	return s.store.GetAccount(ctx, account.ID)
}

func (s *Service) Records(ctx context.Context, accountID string, pageNum, pageSize int, semesterCode, runPlanCode string) (json.RawMessage, error) {
	account, session, unlock, err := s.authenticated(ctx, accountID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	data, err := s.client.Records(ctx, accountDevice(account), session, pageNum, pageSize, semesterCode, runPlanCode)
	if err != nil {
		s.observeSessionError(ctx, &account, err)
	}
	return data, err
}

func (s *Service) RecordDetail(ctx context.Context, accountID, runRecordCode, semesterCode string) (json.RawMessage, error) {
	account, session, unlock, err := s.authenticated(ctx, accountID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	data, err := s.client.RecordDetail(ctx, accountDevice(account), session, runRecordCode, semesterCode)
	if err != nil {
		s.observeSessionError(ctx, &account, err)
	}
	return data, err
}

func (s *Service) RecordInspection(ctx context.Context, accountID, runRecordCode, semesterCode string) (RecordInspection, error) {
	account, session, unlock, err := s.authenticated(ctx, accountID)
	if err != nil {
		return RecordInspection{}, err
	}
	defer unlock()
	detail, err := s.client.RecordDetail(ctx, accountDevice(account), session, runRecordCode, semesterCode)
	if err != nil {
		s.observeSessionError(ctx, &account, err)
		return RecordInspection{}, err
	}
	var rules activity.RunRules
	ruleSource := ""
	if account.DefaultPlanID != nil {
		if plan, planErr := s.store.GetPlan(ctx, *account.DefaultPlanID); planErr == nil && plan.SourceAccountID == account.ID {
			rules, ruleSource = plan.Rules, plan.Name
		}
	}
	if ruleSource == "" {
		if plans, listErr := s.store.ListPlans(ctx); listErr == nil {
			for _, plan := range plans {
				if plan.SourceAccountID == account.ID && (semesterCode == "" || plan.SemesterCode == "" || plan.SemesterCode == semesterCode) {
					rules, ruleSource = plan.Rules, plan.Name
					break
				}
			}
		}
	}
	return RecordInspection{Record: detail, Inspection: activity.InspectRemoteRecord(detail, rules, ruleSource)}, nil
}

func (s *Service) Summary(ctx context.Context, accountID string, timeType, sportType int) (json.RawMessage, error) {
	account, session, unlock, err := s.authenticated(ctx, accountID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	data, err := s.client.RunSummary(ctx, accountDevice(account), session, timeType, sportType)
	if err != nil {
		s.observeSessionError(ctx, &account, err)
	}
	return data, err
}

func (s *Service) SyncResources(ctx context.Context, accountID string) (Resources, error) {
	account, session, unlock, err := s.authenticated(ctx, accountID)
	if err != nil {
		return Resources{}, err
	}
	defer unlock()
	return s.syncResources(ctx, &account, session)
}

func (s *Service) Submit(ctx context.Context, account platform.Account, plan platform.RunningPlan, area platform.RunningArea, request activity.SubmissionRequest) (protocol.SubmissionResult, activity.Run, error) {
	if s.planner == nil || s.generator == nil {
		return protocol.SubmissionResult{}, activity.Run{}, errors.New("起跑后轨迹规划服务未配置")
	}
	stored, session, unlock, err := s.authenticated(ctx, account.ID)
	if err != nil {
		return protocol.SubmissionResult{}, activity.Run{}, err
	}
	defer unlock()
	if plan.SourceAccountID != stored.ID || area.SourceAccountID != stored.ID || plan.RemoteCode == "" || area.RemoteCode == "" {
		return protocol.SubmissionResult{}, activity.Run{}, errors.New("任务计划或区域不是从当前账号同步得到的资源")
	}
	if err := s.ensureRunQuota(ctx, &stored, session, plan, request); err != nil {
		return protocol.SubmissionResult{}, activity.Run{}, err
	}
	// Refresh stale face metadata before a run. A cached/custom image remains
	// usable when the optional refresh fails; SubmitRun decides from preflight
	// whether the selected plan actually requires face verification.
	if stored.FaceSyncedAt == nil || time.Since(*stored.FaceSyncedAt) > time.Hour {
		if _, faceErr := s.syncFaceProfile(ctx, &stored, session); protocol.IsSessionExpired(faceErr) {
			return protocol.SubmissionResult{}, activity.Run{}, faceErr
		}
	}
	faceInput, err := s.effectiveFaceInput(stored)
	if err != nil {
		return protocol.SubmissionResult{}, activity.Run{}, err
	}
	if request.TargetDistanceM <= 0 {
		request.TargetDistanceM = plan.TargetDistanceM
	}
	if request.Profile.TargetPaceSecPerKM <= 0 {
		request.Profile = plan.Profile
	}
	result, run, err := s.client.SubmitRun(
		ctx, accountDevice(stored), session,
		plan.RemoteCode, plan.SemesterCode, area.RemoteCode,
		request.TargetDistanceM, request.Seed, faceInput,
		func(buildContext context.Context, started protocol.RunStartContext) (activity.Run, error) {
			waypoints := selectTargetWaypoints(started.Start, started.ClockMode, started.TargetPoints)
			planned, planErr := s.planner.PlanRoute(buildContext, route.PlanRequest{
				Start: started.Start, Via: waypoints,
				TargetDistanceM: request.TargetDistanceM,
				MaxDistanceM:    plan.Rules.MaxDistanceM,
				CandidateCount:  area.CandidateCount,
				Boundary:        area.Boundary,
				Seed:            request.Seed,
				FairnessKey:     "account:" + stored.ID,
			})
			if planErr != nil {
				return activity.Run{}, planErr
			}
			generated, generateErr := s.generator.Generate(activity.GenerateRequest{
				Route: planned, Profile: request.Profile,
				AccountID: request.AccountID, Seed: &request.Seed,
			})
			if generateErr != nil {
				return activity.Run{}, generateErr
			}
			generated.AutomationJobID = request.AutomationJobID
			return generated, nil
		},
	)
	if err != nil {
		s.observeSessionError(ctx, &stored, err)
		return protocol.SubmissionResult{}, run, err
	}
	run.Submission = &activity.Submission{
		Status: "submitted", RemoteRunID: result.RunRecordCode,
		Message: result.Message, SubmittedAt: result.SubmittedAt,
	}
	return result, run, nil
}

func (s *Service) ensureRunQuota(ctx context.Context, account *platform.Account, session protocol.Session, plan platform.RunningPlan, request activity.SubmissionRequest) error {
	latest, err := s.client.RunRules(ctx, accountDevice(*account), session, plan.SemesterCode, plan.RemoteCode)
	if err != nil {
		s.observeSessionError(ctx, account, err)
		return fmt.Errorf("执行前刷新日/周跑步规则: %w", err)
	}
	dayRule := latest.DayMaxTimes
	if dayRule <= 0 {
		dayRule = plan.Rules.DayMaxTimes
	}
	weekMinRule := latest.WeekMinTimes
	if weekMinRule <= 0 {
		weekMinRule = plan.Rules.WeekMinTimes
	}
	weekMaxRule := latest.WeekMaxTimes
	if weekMaxRule <= 0 {
		weekMaxRule = plan.Rules.WeekMaxTimes
	}
	dayDistanceRuleM := latest.DayMaxDistanceM
	if dayDistanceRuleM <= 0 {
		dayDistanceRuleM = plan.Rules.DayMaxDistanceM
	}
	weekDistanceRuleM := latest.WeekMaxDistanceM
	if weekDistanceRuleM <= 0 {
		weekDistanceRuleM = plan.Rules.WeekMaxDistanceM
	}

	dailyLimit := activity.ResolveDailyRunLimit(request.DailyRunLimit, dayRule)
	weeklyLimit := request.WeeklyRunLimit
	if weeklyLimit <= 0 {
		if weekMinRule > 0 {
			weeklyLimit = weekMinRule
		} else if weekMaxRule > 0 {
			weeklyLimit = weekMaxRule
		}
	}
	if weekMaxRule > 0 && weeklyLimit > weekMaxRule {
		weeklyLimit = weekMaxRule
	}
	dailyDistanceLimitM := request.DailyDistanceLimitM
	if dailyDistanceLimitM <= 0 {
		dailyDistanceLimitM = dayDistanceRuleM
	}
	if dayDistanceRuleM > 0 && dailyDistanceLimitM > dayDistanceRuleM {
		dailyDistanceLimitM = dayDistanceRuleM
	}
	weeklyDistanceLimitM := request.WeeklyDistanceLimitM
	if weeklyDistanceLimitM <= 0 {
		weeklyDistanceLimitM = weekDistanceRuleM
	}
	if weekDistanceRuleM > 0 && weeklyDistanceLimitM > weekDistanceRuleM {
		weeklyDistanceLimitM = weekDistanceRuleM
	}

	timezone := strings.TrimSpace(request.Timezone)
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}
	usage, err := s.client.RunQuotaUsage(ctx, accountDevice(*account), session, plan.SemesterCode, plan.RemoteCode, timezone, time.Now())
	if err != nil {
		s.observeSessionError(ctx, account, err)
		return fmt.Errorf("执行前统计日/周有效记录: %w", err)
	}
	if dailyLimit > 0 && usage.DayCount >= dailyLimit {
		return fmt.Errorf("%w: 今日已完成 %d 次，任务上限为 %d 次", activity.ErrRunQuotaExceeded, usage.DayCount, dailyLimit)
	}
	if weeklyLimit > 0 && usage.WeekCount >= weeklyLimit {
		return fmt.Errorf("%w: 本周已完成 %d 次，任务上限为 %d 次（服务器范围 %d–%d 次）", activity.ErrRunQuotaExceeded, usage.WeekCount, weeklyLimit, weekMinRule, weekMaxRule)
	}
	targetDistanceM := request.TargetDistanceM
	if targetDistanceM <= 0 {
		targetDistanceM = plan.TargetDistanceM
	}
	if dailyDistanceLimitM > 0 && usage.DayDistanceM+targetDistanceM > dailyDistanceLimitM+0.5 {
		return fmt.Errorf("%w: 今日已完成 %.2f km，本次计划 %.2f km，合计将超过 %.2f km 的每日限额", activity.ErrRunQuotaExceeded, usage.DayDistanceM/1000, targetDistanceM/1000, dailyDistanceLimitM/1000)
	}
	if weeklyDistanceLimitM > 0 && usage.WeekDistanceM+targetDistanceM > weeklyDistanceLimitM+0.5 {
		return fmt.Errorf("%w: 本周已完成 %.2f km，本次计划 %.2f km，合计将超过 %.2f km 的每周限额", activity.ErrRunQuotaExceeded, usage.WeekDistanceM/1000, targetDistanceM/1000, weeklyDistanceLimitM/1000)
	}
	return nil
}

// selectTargetWaypoints mirrors the client-visible clock modes while returning
// only coordinates that must influence route generation. The original full
// target-point list remains in the protocol layer for pass-state uploads.
func selectTargetWaypoints(start geo.Point, clockMode int, points []protocol.TargetPoint) []geo.Point {
	if clockMode == protocol.ClockModeNoPoint || len(points) == 0 {
		return nil
	}
	selected := append([]protocol.TargetPoint(nil), points...)
	switch clockMode {
	case protocol.ClockModeRandom:
		// The client requires three sequential random hits. The start list is
		// already ordered by the server-provided sort value.
		if len(selected) > 3 {
			selected = selected[:3]
		}
	case protocol.ClockModeFree:
		required := make([]protocol.TargetPoint, 0, len(selected))
		optional := make([]protocol.TargetPoint, 0, len(selected))
		for _, point := range selected {
			switch point.Type {
			case 1:
				optional = append(optional, point)
			default:
				// Type 2 is the client's required-point type. Unknown non-1
				// values are kept rather than silently dropping a server rule.
				required = append(required, point)
			}
		}
		selected = required
		if len(optional) > 0 {
			best, bestDistance := optional[0], math.Inf(1)
			bases := append([]protocol.TargetPoint(nil), required...)
			for _, candidate := range optional {
				distance := geo.Distance(start, geo.Point{Lat: candidate.Lat, Lng: candidate.Lng})
				for _, base := range bases {
					distance = math.Min(distance, geo.Distance(
						geo.Point{Lat: base.Lat, Lng: base.Lng},
						geo.Point{Lat: candidate.Lat, Lng: candidate.Lng},
					))
				}
				if distance < bestDistance {
					best, bestDistance = candidate, distance
				}
			}
			selected = append(selected, best)
		}
		selected = nearestTargetOrder(start, selected)
	}
	result := make([]geo.Point, 0, len(selected))
	for _, point := range selected {
		coordinate := geo.Point{Lat: point.Lat, Lng: point.Lng}
		if coordinate.Valid() {
			result = append(result, coordinate)
		}
	}
	return result
}

func nearestTargetOrder(start geo.Point, points []protocol.TargetPoint) []protocol.TargetPoint {
	remaining := append([]protocol.TargetPoint(nil), points...)
	ordered := make([]protocol.TargetPoint, 0, len(remaining))
	current := start
	for len(remaining) > 0 {
		bestIndex, bestDistance := 0, math.Inf(1)
		for index, point := range remaining {
			distance := geo.Distance(current, geo.Point{Lat: point.Lat, Lng: point.Lng})
			if distance < bestDistance {
				bestIndex, bestDistance = index, distance
			}
		}
		chosen := remaining[bestIndex]
		ordered = append(ordered, chosen)
		current = geo.Point{Lat: chosen.Lat, Lng: chosen.Lng}
		remaining = append(remaining[:bestIndex], remaining[bestIndex+1:]...)
	}
	return ordered
}

func (s *Service) syncResources(ctx context.Context, account *platform.Account, session protocol.Session) (Resources, error) {
	commonConfig, err := s.client.CommonRunConfig(ctx, accountDevice(*account), session)
	if err != nil {
		s.observeSessionError(ctx, account, err)
		return Resources{}, fmt.Errorf("同步客户端采样/上传配置: %w", err)
	}
	remotePlans, err := s.client.Plans(ctx, accountDevice(*account), session, "")
	if err != nil {
		s.observeSessionError(ctx, account, err)
		return Resources{}, fmt.Errorf("同步账号跑步计划: %w", err)
	}
	remoteAreas, err := s.client.Fences(ctx, accountDevice(*account), session, session.SchoolCode)
	if err != nil {
		s.observeSessionError(ctx, account, err)
		return Resources{}, fmt.Errorf("同步账号跑步区域: %w", err)
	}
	resources := Resources{Plans: make([]platform.RunningPlan, 0, len(remotePlans)), Areas: make([]platform.RunningArea, 0, len(remoteAreas))}
	if face, faceErr := s.syncFaceProfile(ctx, account, session); faceErr == nil {
		resources.Face = face
	} else if protocol.IsSessionExpired(faceErr) {
		return Resources{}, faceErr
	} else {
		resources.Face = account.FaceProfile()
		resources.FaceSyncError = faceErr.Error()
	}
	for _, remote := range remotePlans {
		remoteRules, rulesErr := s.client.RunRules(ctx, accountDevice(*account), session, remote.SemesterCode, remote.RunPlanCode)
		if rulesErr != nil {
			s.observeSessionError(ctx, account, rulesErr)
			return Resources{}, fmt.Errorf("读取计划 %s 的距离/配速/步频/步幅规则: %w", remote.RunPlanCode, rulesErr)
		}
		rules := activity.RunRules{
			PlanType: remoteRules.PlanType, TotalDistanceM: remoteRules.TotalDistanceM, TotalTimes: remoteRules.TotalTimes,
			DayMaxTimes: remoteRules.DayMaxTimes, DayMaxDistanceM: remoteRules.DayMaxDistanceM,
			WeekMinTimes: remoteRules.WeekMinTimes, WeekMaxTimes: remoteRules.WeekMaxTimes, WeekMaxDistanceM: remoteRules.WeekMaxDistanceM,
			CompletionRate: remoteRules.CompletionRate, DoneDistanceM: remoteRules.DoneDistanceM,
			DoneTargetTimes: remoteRules.DoneTargetTimes, MorningTimes: remoteRules.MorningTimes,
			MinDistanceM: remoteRules.MinDistanceM, MaxDistanceM: remoteRules.MaxDistanceM,
			FastestPaceSecPerKM: remoteRules.FastestPaceSecPerKM, SlowestPaceSecPerKM: remoteRules.SlowestPaceSecPerKM,
			MinCadenceSPM: remoteRules.MinCadenceSPM, MaxCadenceSPM: remoteRules.MaxCadenceSPM,
			MaxSegmentStrideM:        remoteRules.MaxSegmentStrideM,
			SegmentStrideExceptions:  remoteRules.SegmentStrideExceptions,
			SwingAmplitudeThreshold:  remoteRules.SwingAmplitudeThreshold,
			LocationSampleIntervalS:  3,
			POIUploadIntervalS:       commonConfig.POIUploadIntervalS,
			PaceUploadCheckIntervalS: 15,
			PaceSegmentDistanceM:     commonConfig.PaceSegmentDistanceM,
			StepSegmentIntervalS:     commonConfig.StepSegmentIntervalS,
			StrideSegmentDistanceM:   commonConfig.StrideSegmentDistanceM,
			ActiveStartDate:          remoteRules.ActiveStartDate,
			ActiveEndDate:            remoteRules.ActiveEndDate,
			TimeWindows:              remoteRules.TimeWindows,
			Source:                   "common/getConfig + run/querySunRunAbstractInfoV2.planType/schoolDemandRule",
		}
		id := stableResourceID("plan", account.ID, remote.RunPlanCode)
		baseProfile := activity.Profile{}
		if existing, getErr := s.store.GetPlan(ctx, id); getErr == nil {
			baseProfile = existing.Profile
		} else if !errors.Is(getErr, store.ErrNotFound) {
			return Resources{}, getErr
		}
		fallbackDistance := remote.TargetDistance
		if fallbackDistance < 300 {
			fallbackDistance = rules.MinDistanceM
		}
		selection, sampleErr := activity.SampleTargetsForDistance(fallbackDistance, baseProfile, rules, 0, time.Now().UnixNano())
		if sampleErr != nil {
			return Resources{}, fmt.Errorf("生成计划 %s 的规则内参数: %w", remote.RunPlanCode, sampleErr)
		}
		selection.Profile.SampleIntervalS = rules.LocationSampleIntervalS
		description := "账号规则已自动同步；距离按 100 米档位选择并默认使用最高档，配速/步频/步幅在规则中间 70% 联动抽样"
		if rules.PlanType == activity.PlanTypeByTimes {
			description = fmt.Sprintf("账号规则已自动同步；按次数考核，自动任务每次使用最低距离 %.1f km，配速/步频/步幅在规则中间 70%% 联动抽样", rules.MinDistanceM/1000)
		} else if rules.PlanType == activity.PlanTypeByDistance {
			description = "账号规则已自动同步；按公里数考核，距离按 100 米档位选择并默认使用最高档，配速/步频/步幅在规则中间 70% 联动抽样"
		}
		plan := platform.RunningPlan{
			ID: id, SourceAccountID: account.ID,
			RemoteCode: remote.RunPlanCode, SemesterCode: remote.SemesterCode,
			Name: remote.Name, Description: description,
			TargetDistanceM: selection.TargetDistanceM, Profile: selection.Profile, Rules: rules,
		}
		if plan.Name == "" {
			plan.Name = "计划 " + remote.RunPlanCode
		}
		if _, getErr := s.store.GetPlan(ctx, plan.ID); getErr == nil {
			plan, err = s.store.UpdatePlan(ctx, plan)
		} else if errors.Is(getErr, store.ErrNotFound) {
			plan, err = s.store.CreatePlan(ctx, plan)
		} else {
			err = getErr
		}
		if err != nil {
			return Resources{}, err
		}
		resources.Plans = append(resources.Plans, plan)
	}
	for _, remote := range remoteAreas {
		boundary := make([]geo.Point, 0, len(remote.FenceList))
		for _, point := range remote.FenceList {
			boundary = append(boundary, geo.Point{Lat: point.Lat, Lng: point.Lng})
		}
		center := geo.Point{Lat: remote.Lat, Lng: remote.Lng}
		if !center.Valid() && len(boundary) > 0 {
			center = polygonCenter(boundary)
		}
		if !center.Valid() {
			continue
		}
		area := platform.RunningArea{
			ID: stableResourceID("area", account.ID, remote.FenceCode), SourceAccountID: account.ID,
			RemoteCode: remote.FenceCode, SchoolCode: remote.SchoolCode, SubSchoolCode: remote.SubSchoolCode,
			Name: remote.FenceName, Description: "从账号同步的学校围栏区域", Center: center,
			Boundary: boundary, HeadingDeg: 0, CandidateCount: 4,
		}
		if area.Name == "" {
			area.Name = remote.SchoolName
		}
		if area.Name == "" {
			area.Name = "围栏 " + remote.FenceCode
		}
		if existing, getErr := s.store.GetArea(ctx, area.ID); getErr == nil {
			area.HeadingDeg = existing.HeadingDeg
			area.CandidateCount = existing.CandidateCount
			area, err = s.store.UpdateArea(ctx, area)
		} else if errors.Is(getErr, store.ErrNotFound) {
			area, err = s.store.CreateArea(ctx, area)
		} else {
			err = getErr
		}
		if err != nil {
			return Resources{}, err
		}
		resources.Areas = append(resources.Areas, area)
	}
	changed := false
	if account.DefaultPlanID == nil || !containsPlan(resources.Plans, *account.DefaultPlanID) {
		if len(resources.Plans) > 0 {
			account.DefaultPlanID = &resources.Plans[0].ID
			changed = true
		}
	}
	if account.DefaultAreaID == nil || !containsArea(resources.Areas, *account.DefaultAreaID) {
		if len(resources.Areas) > 0 {
			account.DefaultAreaID = &resources.Areas[0].ID
			changed = true
		}
	}
	if changed {
		if _, err := s.store.UpdateAccount(ctx, *account); err != nil {
			return Resources{}, err
		}
	}
	return resources, nil
}

func stableResourceID(prefix, accountID, remoteCode string) string {
	digest := sha256.Sum256([]byte(accountID + "\x00" + remoteCode))
	return prefix + "_remote_" + hex.EncodeToString(digest[:8])
}

func polygonCenter(points []geo.Point) geo.Point {
	var lat, lng float64
	for _, point := range points {
		lat += point.Lat
		lng += point.Lng
	}
	return geo.Point{Lat: lat / float64(len(points)), Lng: lng / float64(len(points))}
}

func containsPlan(plans []platform.RunningPlan, id string) bool {
	for _, plan := range plans {
		if plan.ID == id {
			return true
		}
	}
	return false
}

func containsArea(areas []platform.RunningArea, id string) bool {
	for _, area := range areas {
		if area.ID == id {
			return true
		}
	}
	return false
}

func (s *Service) authenticated(ctx context.Context, accountID string) (platform.Account, protocol.Session, func(), error) {
	unlock := s.lock(accountID)
	account, err := s.store.GetAccount(ctx, accountID)
	if err != nil {
		unlock()
		return platform.Account{}, protocol.Session{}, func() {}, err
	}
	session, err := s.loadSession(account)
	if err == nil && session.Token != "" && session.SAToken != "" {
		validated, validateErr := s.client.ValidateSession(ctx, accountDevice(account), session)
		if validateErr == nil {
			if validated.UserName != session.UserName || validated.StudentNumber != session.StudentNumber || validated.SchoolCode != session.SchoolCode || validated.SchoolName != session.SchoolName {
				account, err = s.persistSession(ctx, account, validated)
				if err != nil {
					unlock()
					return platform.Account{}, protocol.Session{}, func() {}, err
				}
			}
			return account, validated, unlock, nil
		}
		s.observeSessionError(ctx, &account, validateErr)
		unlock()
		return platform.Account{}, protocol.Session{}, func() {}, validateErr
	}
	if err == nil {
		err = errors.New("账号尚未登录，请重新登录")
		if account.AuthStatus != "expired" {
			if len(account.PasswordCipher) > 0 {
				account.AuthStatus = "configured"
			} else {
				account.AuthStatus = "unconfigured"
			}
			account.AuthMessage = err.Error()
			_, _ = s.store.UpdateAccount(context.WithoutCancel(ctx), account)
		}
	} else {
		s.recordAuthError(ctx, &account, err)
	}
	unlock()
	return platform.Account{}, protocol.Session{}, func() {}, err
}

func (s *Service) persistSession(ctx context.Context, account platform.Account, session protocol.Session) (platform.Account, error) {
	account, err := s.attachSession(account, session)
	if err != nil {
		return platform.Account{}, err
	}
	return s.store.UpdateAccount(ctx, account)
}

func (s *Service) attachSession(account platform.Account, session protocol.Session) (platform.Account, error) {
	raw, err := json.Marshal(session)
	if err != nil {
		return platform.Account{}, err
	}
	account.SessionCipher, err = s.vault.Encrypt(raw, sessionAAD(account.ID))
	if err != nil {
		return platform.Account{}, err
	}
	now := time.Now().UTC()
	account.AuthStatus = "authenticated"
	account.AuthMessage = "会话有效"
	account.LastAuthAt = &now
	if session.UserName != "" {
		account.Name = session.UserName
	}
	account.StudentNumber = session.StudentNumber
	account.SchoolCode = session.SchoolCode
	account.SchoolName = session.SchoolName
	account.PasswordConfigured = len(account.PasswordCipher) > 0
	account.SessionConfigured = len(account.SessionCipher) > 0
	return account, nil
}

func (s *Service) prepareOnboardingAccount(account platform.Account) platform.Account {
	account.ID = ""
	account.DefaultPlanID = nil
	account.DefaultAreaID = nil
	account.PasswordCipher = nil
	account.SessionCipher = nil
	account.FaceBaseURL = ""
	account.FaceBaseCipher = nil
	account.FaceBaseMIME = ""
	account.FaceCustomCipher = nil
	account.FaceCustomMIME = ""
	account.FaceSyncedAt = nil
	account.PasswordConfigured = false
	account.SessionConfigured = false
	account.AuthStatus = ""
	account.AuthMessage = ""
	account.LastAuthAt = nil
	account.CreatedAt = time.Time{}
	account.UpdatedAt = time.Time{}
	account.Enabled = true
	s.applyDefaults(&account)
	return account
}

func (s *Service) ensureLoginAvailable(ctx context.Context, loginName string) error {
	accounts, err := s.store.ListAccounts(ctx)
	if err != nil {
		return err
	}
	loginName = strings.TrimSpace(loginName)
	for _, account := range accounts {
		if strings.EqualFold(strings.TrimSpace(account.LoginName), loginName) {
			return errors.New("该登录账号已存在")
		}
	}
	return nil
}

func (s *Service) createAuthenticatedAccount(ctx context.Context, account platform.Account, password string, session protocol.Session) (platform.Account, error) {
	account.ID = platform.NewID("acct")
	s.applyDefaults(&account)
	if strings.TrimSpace(password) != "" {
		ciphertext, err := s.vault.Encrypt([]byte(password), passwordAAD(account.ID))
		if err != nil {
			return platform.Account{}, err
		}
		account.PasswordCipher = ciphertext
	}
	prepared, err := s.attachSession(account, session)
	if err != nil {
		return platform.Account{}, err
	}
	created, err := s.store.CreateAccount(ctx, prepared)
	if err != nil {
		return platform.Account{}, err
	}
	if _, syncErr := s.syncResources(ctx, &created, session); syncErr != nil {
		if latest, latestErr := s.store.GetAccount(context.WithoutCancel(ctx), created.ID); latestErr == nil {
			created = latest
		}
		if created.AuthStatus != "expired" {
			created.AuthMessage = "登录成功；账号资料可稍后重新读取"
			_, _ = s.store.UpdateAccount(context.WithoutCancel(ctx), created)
		}
	}
	refreshed, getErr := s.store.GetAccount(context.WithoutCancel(ctx), created.ID)
	if getErr != nil {
		return created, nil
	}
	return refreshed, nil
}

func (s *Service) loadSession(account platform.Account) (protocol.Session, error) {
	if len(account.SessionCipher) == 0 {
		return protocol.Session{}, nil
	}
	raw, err := s.vault.Decrypt(account.SessionCipher, sessionAAD(account.ID))
	if err != nil {
		return protocol.Session{}, err
	}
	var session protocol.Session
	if err := json.Unmarshal(raw, &session); err != nil {
		return protocol.Session{}, fmt.Errorf("解析账号会话: %w", err)
	}
	return session, nil
}

func (s *Service) recordAuthError(ctx context.Context, account *platform.Account, cause error) {
	if account.AuthStatus != "expired" {
		account.AuthStatus = "error"
	}
	account.AuthMessage = cause.Error()
	_, _ = s.store.UpdateAccount(context.WithoutCancel(ctx), *account)
}

// observeSessionError turns any upstream login-expiry response into durable
// account state. It deliberately does not retry here: re-authentication is an
// explicit account action so scheduled or concurrent requests cannot create a
// burst of password/SMS login attempts.
func (s *Service) observeSessionError(ctx context.Context, account *platform.Account, cause error) bool {
	if account == nil || !protocol.IsSessionExpired(cause) {
		return false
	}
	account.SessionCipher = nil
	account.SessionConfigured = false
	account.AuthStatus = "expired"
	account.AuthMessage = "登录状态已失效，请重新登录"
	if updated, err := s.store.UpdateAccount(context.WithoutCancel(ctx), *account); err == nil {
		*account = updated
	}
	return true
}

func (s *Service) applyDefaults(account *platform.Account) {
	account.LoginName = strings.TrimSpace(account.LoginName)
	if account.Name == "" {
		account.Name = account.LoginName
	}
	if account.DeviceID == "" {
		var value [8]byte
		if _, err := rand.Read(value[:]); err == nil {
			account.DeviceID = hex.EncodeToString(value[:])
		} else {
			account.DeviceID = strings.TrimPrefix(platform.NewID(""), "_")
		}
	}
	if account.ModelName == "" {
		account.ModelName = "Redmi|23013RK75C"
	}
	if account.SystemVersion == "" {
		account.SystemVersion = "14"
	}
	if account.AuthStatus == "" {
		if len(account.SessionCipher) > 0 {
			account.AuthStatus = "authenticated"
		} else if len(account.PasswordCipher) > 0 {
			account.AuthStatus = "configured"
		} else {
			account.AuthStatus = "unconfigured"
		}
	}
}

func (s *Service) lock(accountID string) func() {
	value, _ := s.locks.LoadOrStore(accountID, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

func accountDevice(account platform.Account) protocol.Device {
	return protocol.Device{ID: account.DeviceID, ModelName: account.ModelName, SystemVersion: account.SystemVersion}
}

func passwordAAD(accountID string) string { return "account:" + accountID + ":password" }
func sessionAAD(accountID string) string  { return "account:" + accountID + ":session" }
