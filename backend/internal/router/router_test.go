package router_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/blueship581/print-color-calibration-release/backend/internal/config"
	"github.com/blueship581/print-color-calibration-release/backend/internal/database"
	"github.com/blueship581/print-color-calibration-release/backend/internal/router"
	"github.com/gin-gonic/gin"
)

type apiEnvelope struct {
	Data json.RawMessage `json:"data"`
}

func TestRBACAndImmutableRevisionFlows(t *testing.T) {
	cfg := testConfig(filepath.Join(t.TempDir(), "gb517.db"))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, redisClient, err := database.Open(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	engine := router.New(cfg, db, redisClient, logger)
	tokens := map[string]string{}
	for _, role := range []string{"viewer", "operator", "reviewer", "admin"} {
		tokens[role] = loginToken(t, engine, role)
	}

	payload := recordPayload("RD-TEST-001", "测试放行决定")
	if status, _ := perform(t, engine, http.MethodPost, "/api/release", tokens["viewer"], "viewer-create", payload); status != http.StatusForbidden {
		t.Fatalf("viewer create status = %d, want 403", status)
	}

	// A release decision must belong to a run and select accepted proofs of
	// the run's current configuration version. Build that evidence first.
	decisionRunPayload := recordPayload("PR-DECISION-001", "放行目标色彩配置")
	status, body := perform(t, engine, http.MethodPost, "/api/runs", tokens["operator"], "decision-run-create", decisionRunPayload)
	decisionRun := decodeData[struct {
		ID      uint `json:"id"`
		Version uint `json:"version"`
	}](t, body)
	if status != http.StatusCreated {
		t.Fatalf("create decision run status = %d body=%s", status, body)
	}
	proofPayload := recordPayload("CP-DECISION-001", "放行目标校样")
	proofPayload["printRunId"] = decisionRun.ID
	status, body = perform(t, engine, http.MethodPost, "/api/proofs", tokens["operator"], "decision-proof-create", proofPayload)
	if status != http.StatusCreated {
		t.Fatalf("create decision proof status = %d body=%s", status, body)
	}
	decisionProof := decodeData[struct {
		ID      uint `json:"id"`
		Version uint `json:"version"`
	}](t, body)
	for _, step := range []struct {
		status  string
		reason  string
		token   string
		request string
	}{
		{"review", "measurement capture completed", tokens["operator"], "decision-proof-review"},
		{"accepted", "colour tolerance independently verified", tokens["reviewer"], "decision-proof-accept"},
	} {
		transition := map[string]any{"status": step.status, "expectedVersion": decisionProof.Version, "reason": step.reason}
		status, body = perform(t, engine, http.MethodPost, "/api/proofs/"+uintString(decisionProof.ID)+"/transition", step.token, step.request, transition)
		if status != http.StatusOK {
			t.Fatalf("proof transition to %s status = %d body=%s", step.status, status, body)
		}
		decisionProof.Version = decodeData[struct {
			Version uint `json:"version"`
		}](t, body).Version
	}
	status, body = perform(t, engine, http.MethodGet, "/api/proofs/"+uintString(decisionProof.ID), tokens["reviewer"], "decision-proof-read", nil)
	acceptedProof := decodeData[struct {
		Stale             bool    `json:"stale"`
		PinnedRunCode     string  `json:"pinnedRunCode"`
		PinnedRunVersion  uint    `json:"pinnedRunVersion"`
		PinnedMetricValue float64 `json:"pinnedMetricValue"`
	}](t, body)
	if status != http.StatusOK || acceptedProof.Stale || acceptedProof.PinnedRunCode != "PR-DECISION-001" || acceptedProof.PinnedRunVersion != 1 || acceptedProof.PinnedMetricValue != 2.1 {
		t.Fatalf("accepted proof pinning mismatch: status=%d %+v", status, acceptedProof)
	}
	// An accepted proof is pinned evidence and cannot be edited anymore.
	proofUpdate := recordPayload("ignored", "试图覆盖已接收读数")
	proofUpdate["expectedVersion"] = decisionProof.Version
	if status, _ := perform(t, engine, http.MethodPut, "/api/proofs/"+uintString(decisionProof.ID), tokens["operator"], "accepted-proof-update", proofUpdate); status != http.StatusConflict {
		t.Fatalf("accepted proof update status = %d, want 409", status)
	}

	payload["printRunId"] = decisionRun.ID
	payload["proofIds"] = []uint{decisionProof.ID}
	status, body = perform(t, engine, http.MethodPost, "/api/release", tokens["operator"], "decision-create", payload)
	if status != http.StatusCreated {
		t.Fatalf("operator create decision status = %d body=%s", status, body)
	}
	decision := decodeData[struct {
		ID      uint `json:"id"`
		Version uint `json:"version"`
	}](t, body)
	transition := map[string]any{"status": "release", "expectedVersion": decision.Version, "reason": "quality gate accepted"}
	path := "/api/release/" + uintString(decision.ID) + "/transition"
	if status, _ := perform(t, engine, http.MethodPost, path, tokens["operator"], "operator-release", transition); status != http.StatusForbidden {
		t.Fatalf("operator release status = %d, want 403", status)
	}
	status, body = perform(t, engine, http.MethodPost, path, tokens["reviewer"], "reviewer-release", transition)
	if status != http.StatusOK {
		t.Fatalf("reviewer release status = %d body=%s", status, body)
	}
	status, body = perform(t, engine, http.MethodGet, "/api/release/"+uintString(decision.ID), tokens["reviewer"], "decision-read", nil)
	detail := decodeData[struct {
		Version   uint `json:"version"`
		Revisions []struct {
			Version        uint   `json:"version"`
			RequestID      string `json:"requestId"`
			ProofSnapshots []struct {
				ProofCode         string  `json:"proofCode"`
				PinnedRunVersion  uint    `json:"pinnedRunVersion"`
				PinnedMetricValue float64 `json:"pinnedMetricValue"`
				Stale             bool    `json:"stale"`
			} `json:"proofSnapshots"`
		} `json:"revisions"`
	}](t, body)
	if status != http.StatusOK || detail.Version != 2 || len(detail.Revisions) != 2 || detail.Revisions[0].RequestID != "reviewer-release" {
		t.Fatalf("unexpected decision revision chain: status=%d detail=%+v", status, detail)
	}
	if snapshots := detail.Revisions[0].ProofSnapshots; len(snapshots) != 1 || snapshots[0].ProofCode != "CP-DECISION-001" ||
		snapshots[0].PinnedRunVersion != 1 || snapshots[0].PinnedMetricValue != 2.1 || snapshots[0].Stale {
		t.Fatalf("release revision must keep the eligible proof snapshot: %+v", snapshots)
	}
	update := recordPayload("ignored", "不得覆盖的决定")
	update["expectedVersion"] = detail.Version
	update["printRunId"] = decisionRun.ID
	update["proofIds"] = []uint{decisionProof.ID}
	if status, _ := perform(t, engine, http.MethodPut, "/api/release/"+uintString(decision.ID), tokens["operator"], "locked-update", update); status != http.StatusConflict {
		t.Fatalf("resolved decision update status = %d, want 409", status)
	}
	if status, _ := perform(t, engine, http.MethodDelete, "/api/release/"+uintString(decision.ID), tokens["admin"], "locked-delete", nil); status != http.StatusConflict {
		t.Fatalf("resolved decision delete status = %d, want 409", status)
	}

	// After the run configuration is revised, the old accepted proof becomes
	// stale and a second decision may not select it for release.
	runUpdate := recordPayload("ignored", "改版后的色彩配置")
	runUpdate["expectedVersion"] = decisionRun.Version
	if status, _ := perform(t, engine, http.MethodPut, "/api/runs/"+uintString(decisionRun.ID), tokens["operator"], "decision-run-revise", runUpdate); status != http.StatusOK {
		t.Fatalf("revise decision run status = %d, want 200", status)
	}
	status, body = perform(t, engine, http.MethodGet, "/api/proofs/"+uintString(decisionProof.ID), tokens["reviewer"], "stale-proof-read", nil)
	staleProof := decodeData[struct {
		Stale bool `json:"stale"`
	}](t, body)
	if status != http.StatusOK || !staleProof.Stale {
		t.Fatalf("proof must be stale after run revision: status=%d %+v", status, staleProof)
	}
	stalePayload := recordPayload("RD-TEST-STALE", "旧校样不得放行")
	stalePayload["printRunId"] = decisionRun.ID
	stalePayload["proofIds"] = []uint{decisionProof.ID}
	if status, _ := perform(t, engine, http.MethodPost, "/api/release", tokens["reviewer"], "stale-decision-create", stalePayload); status != http.StatusUnprocessableEntity {
		t.Fatalf("release decision with stale proof status = %d, want 422", status)
	}

	runPayload := recordPayload("PR-TEST-001", "测试色彩配置")
	status, body = perform(t, engine, http.MethodPost, "/api/runs", tokens["operator"], "run-create", runPayload)
	run := decodeData[struct {
		ID      uint `json:"id"`
		Version uint `json:"version"`
	}](t, body)
	if status != http.StatusCreated {
		t.Fatalf("create run status = %d body=%s", status, body)
	}
	runPath := "/api/runs/" + uintString(run.ID) + "/transition"
	status, _ = perform(t, engine, http.MethodPost, runPath, tokens["operator"], "run-printing", map[string]any{"status": "printing", "expectedVersion": run.Version, "reason": "plates and ink verified"})
	if status != http.StatusOK {
		t.Fatalf("run transition status = %d", status)
	}
	if status, _ := perform(t, engine, http.MethodDelete, "/api/runs/"+uintString(run.ID), tokens["admin"], "locked-run-delete", nil); status != http.StatusConflict {
		t.Fatalf("active run delete status = %d, want 409", status)
	}
	_, body = perform(t, engine, http.MethodGet, "/api/runs/"+uintString(run.ID), tokens["operator"], "run-read", nil)
	runDetail := decodeData[struct {
		Revisions []struct {
			RequestID string `json:"requestId"`
		} `json:"revisions"`
	}](t, body)
	if len(runDetail.Revisions) != 2 || runDetail.Revisions[0].RequestID != "run-printing" {
		t.Fatalf("unexpected colour configuration revisions: %+v", runDetail.Revisions)
	}

	if status, _ := perform(t, engine, http.MethodGet, "/api/audits", tokens["viewer"], "viewer-audit", nil); status != http.StatusForbidden {
		t.Fatalf("viewer audit status = %d, want 403", status)
	}
	if status, _ := perform(t, engine, http.MethodGet, "/api/audits", tokens["reviewer"], "reviewer-audit", nil); status != http.StatusOK {
		t.Fatalf("reviewer audit status = %d, want 200", status)
	}
}

func testConfig(dsn string) config.Config {
	return config.Config{
		AppName: "print-color-calibration-release", Environment: "test", Port: "0",
		DatabaseDriver: "sqlite", DatabaseDSN: dsn, JWTSecret: "gb517-router-tests-secret",
		TokenTTL: time.Hour, RequestLimit: 1000, StartupTimeout: time.Second,
		ShutdownTimeout: time.Second, ReadHeaderTimeout: time.Second, ReadTimeout: time.Second,
		WriteTimeout: time.Second, IdleTimeout: time.Second,
	}
}

func loginToken(t *testing.T, engine *gin.Engine, username string) string {
	t.Helper()
	status, body := perform(t, engine, http.MethodPost, "/api/auth/login", "", "login-"+username, map[string]any{"username": username, "password": "Admin123!"})
	if status != http.StatusOK {
		t.Fatalf("login %s status = %d body=%s", username, status, body)
	}
	return decodeData[struct {
		Token string `json:"token"`
	}](t, body).Token
}

func recordPayload(code, name string) map[string]any {
	return map[string]any{
		"code": code, "name": name, "description": "router integration test",
		"facility": "测试印刷区", "owner": "operator", "category": "校准",
		"riskLevel": "medium", "metricValue": 2.1, "metricUnit": "dE",
		"effectiveAt": time.Now().UTC().Format(time.RFC3339), "evidence": "spectrophotometer evidence", "relatedCode": "PR-001",
	}
}

func perform(t *testing.T, engine *gin.Engine, method, path, token, requestID string, payload any) (int, []byte) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		body = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, body)
	request.Header.Set("X-Request-ID", requestID)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response.Code, response.Body.Bytes()
}

func decodeData[T any](t *testing.T, body []byte) T {
	t.Helper()
	var envelope apiEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope %s: %v", body, err)
	}
	var value T
	if err := json.Unmarshal(envelope.Data, &value); err != nil {
		t.Fatalf("decode data %s: %v", envelope.Data, err)
	}
	return value
}

func uintString(value uint) string {
	return strconv.FormatUint(uint64(value), 10)
}
