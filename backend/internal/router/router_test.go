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

	// A release decision may only cross the release gate with a proof that was
	// accepted under the current version of its batch.
	proofPayload := recordPayload("CP-TEST-001", "测试放行校样")
	status, body := perform(t, engine, http.MethodPost, "/api/proofs", tokens["operator"], "proof-create", proofPayload)
	if status != http.StatusCreated {
		t.Fatalf("operator create proof status = %d body=%s", status, body)
	}
	proof := decodeData[struct {
		ID      uint `json:"id"`
		Version uint `json:"version"`
	}](t, body)
	proofPath := "/api/proofs/" + uintString(proof.ID) + "/transition"
	if status, _ := perform(t, engine, http.MethodPost, proofPath, tokens["operator"], "proof-review", map[string]any{"status": "review", "expectedVersion": proof.Version, "reason": "measurement capture completed"}); status != http.StatusOK {
		t.Fatalf("proof review status = %d", status)
	}
	status, body = perform(t, engine, http.MethodPost, proofPath, tokens["reviewer"], "proof-accept", map[string]any{"status": "accepted", "expectedVersion": 2, "reason": "colour tolerance independently verified"})
	if status != http.StatusOK {
		t.Fatalf("proof accept status = %d body=%s", status, body)
	}

	payload := recordPayload("RD-TEST-001", "测试放行决定")
	payload["proofId"] = proof.ID
	if status, _ := perform(t, engine, http.MethodPost, "/api/release", tokens["viewer"], "viewer-create", payload); status != http.StatusForbidden {
		t.Fatalf("viewer create status = %d, want 403", status)
	}
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
			Version   uint   `json:"version"`
			RequestID string `json:"requestId"`
		} `json:"revisions"`
	}](t, body)
	if status != http.StatusOK || detail.Version != 2 || len(detail.Revisions) != 2 || detail.Revisions[0].RequestID != "reviewer-release" {
		t.Fatalf("unexpected decision revision chain: status=%d detail=%+v", status, detail)
	}
	update := recordPayload("ignored", "不得覆盖的决定")
	update["expectedVersion"] = detail.Version
	if status, _ := perform(t, engine, http.MethodPut, "/api/release/"+uintString(decision.ID), tokens["operator"], "locked-update", update); status != http.StatusConflict {
		t.Fatalf("resolved decision update status = %d, want 409", status)
	}
	if status, _ := perform(t, engine, http.MethodDelete, "/api/release/"+uintString(decision.ID), tokens["admin"], "locked-delete", nil); status != http.StatusConflict {
		t.Fatalf("resolved decision delete status = %d, want 409", status)
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

// TestProofAcceptancePinsBatchAndReleaseGate covers the proof lifecycle around
// batch revisions: acceptance pins the batch code/version/readings, a later
// configuration revision invalidates the proof, the release gate refuses stale
// proofs, and the decision keeps its proof snapshot immutable afterwards.
func TestProofAcceptancePinsBatchAndReleaseGate(t *testing.T) {
	cfg := testConfig(filepath.Join(t.TempDir(), "gb517-proof.db"))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, redisClient, err := database.Open(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	engine := router.New(cfg, db, redisClient, logger)
	tokens := map[string]string{}
	for _, role := range []string{"operator", "reviewer"} {
		tokens[role] = loginToken(t, engine, role)
	}

	runPayload := recordPayload("PR-GATE-001", "批次版本固定测试")
	status, body := perform(t, engine, http.MethodPost, "/api/runs", tokens["operator"], "gate-run-create", runPayload)
	if status != http.StatusCreated {
		t.Fatalf("create run status = %d body=%s", status, body)
	}
	run := decodeData[struct {
		ID      uint `json:"id"`
		Version uint `json:"version"`
	}](t, body)

	proofPayload := recordPayload("CP-GATE-001", "校样接收固定测试")
	proofPayload["relatedCode"] = "PR-GATE-001"
	status, body = perform(t, engine, http.MethodPost, "/api/proofs", tokens["operator"], "gate-proof-create", proofPayload)
	if status != http.StatusCreated {
		t.Fatalf("create proof status = %d body=%s", status, body)
	}
	proof := decodeData[struct {
		ID      uint `json:"id"`
		Version uint `json:"version"`
	}](t, body)
	proofPath := "/api/proofs/" + uintString(proof.ID) + "/transition"
	status, body = perform(t, engine, http.MethodPost, proofPath, tokens["operator"], "gate-proof-review", map[string]any{"status": "review", "expectedVersion": proof.Version, "reason": "measurement capture completed"})
	if status != http.StatusOK {
		t.Fatalf("proof review status = %d body=%s", status, body)
	}
	status, body = perform(t, engine, http.MethodPost, proofPath, tokens["reviewer"], "gate-proof-accept", map[string]any{"status": "accepted", "expectedVersion": 2, "reason": "colour tolerance independently verified"})
	if status != http.StatusOK {
		t.Fatalf("proof accept status = %d body=%s", status, body)
	}
	accepted := decodeData[struct {
		Version       uint    `json:"version"`
		RunCode       string  `json:"runCode"`
		RunVersion    uint    `json:"runVersion"`
		AcceptedValue float64 `json:"acceptedValue"`
		AcceptedUnit  string  `json:"acceptedUnit"`
		AcceptedBy    string  `json:"acceptedBy"`
		Stale         bool    `json:"stale"`
	}](t, body)
	if accepted.RunCode != "PR-GATE-001" || accepted.RunVersion != 1 || accepted.AcceptedValue != 2.1 ||
		accepted.AcceptedUnit != "dE" || accepted.AcceptedBy != "reviewer" || accepted.Stale {
		t.Fatalf("acceptance did not pin batch snapshot: %+v", accepted)
	}

	decisionPayload := recordPayload("RD-GATE-001", "放行门控测试")
	decisionPayload["relatedCode"] = "PR-GATE-001"
	decisionPayload["proofId"] = proof.ID
	status, body = perform(t, engine, http.MethodPost, "/api/release", tokens["operator"], "gate-decision-create", decisionPayload)
	if status != http.StatusCreated {
		t.Fatalf("create decision status = %d body=%s", status, body)
	}
	decision := decodeData[struct {
		ID              uint    `json:"id"`
		Version         uint    `json:"version"`
		ProofCode       string  `json:"proofCode"`
		ProofRunVersion uint    `json:"proofRunVersion"`
		ProofValue      float64 `json:"proofValue"`
	}](t, body)
	if decision.ProofCode != "CP-GATE-001" || decision.ProofRunVersion != 1 || decision.ProofValue != 2.1 {
		t.Fatalf("decision did not snapshot the proof: %+v", decision)
	}

	// A configuration revision bumps the batch version and invalidates proofs
	// accepted under the previous version.
	runUpdate := recordPayload("ignored", "改版后的色彩配置")
	runUpdate["expectedVersion"] = run.Version
	status, body = perform(t, engine, http.MethodPut, "/api/runs/"+uintString(run.ID), tokens["operator"], "gate-run-revise", runUpdate)
	if status != http.StatusOK {
		t.Fatalf("revise run status = %d body=%s", status, body)
	}
	status, body = perform(t, engine, http.MethodGet, "/api/proofs/"+uintString(proof.ID), tokens["operator"], "gate-proof-read", nil)
	if status != http.StatusOK {
		t.Fatalf("read proof status = %d", status)
	}
	staleProof := decodeData[struct {
		Version uint `json:"version"`
		Stale   bool `json:"stale"`
	}](t, body)
	if !staleProof.Stale {
		t.Fatalf("proof should be invalidated after batch revision: %+v", staleProof)
	}

	// Stale proofs can neither be selected for a new decision nor released.
	staleDecisionPayload := recordPayload("RD-GATE-002", "失效校样选择")
	staleDecisionPayload["relatedCode"] = "PR-GATE-001"
	staleDecisionPayload["proofId"] = proof.ID
	if status, _ = perform(t, engine, http.MethodPost, "/api/release", tokens["operator"], "gate-stale-select", staleDecisionPayload); status != http.StatusUnprocessableEntity {
		t.Fatalf("stale proof select status = %d, want 422", status)
	}
	releasePath := "/api/release/" + uintString(decision.ID) + "/transition"
	if status, _ = perform(t, engine, http.MethodPost, releasePath, tokens["reviewer"], "gate-stale-release", map[string]any{"status": "release", "expectedVersion": decision.Version, "reason": "attempt release with stale proof"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("stale proof release status = %d, want 422", status)
	}

	// Re-accepting the proof pins the current batch version and makes it usable.
	proofVersion := staleProof.Version
	status, body = perform(t, engine, http.MethodPost, proofPath, tokens["reviewer"], "gate-proof-rework", map[string]any{"status": "review", "expectedVersion": proofVersion, "reason": "re-measure after configuration revision"})
	if status != http.StatusOK {
		t.Fatalf("proof rework status = %d body=%s", status, body)
	}
	proofVersion++
	status, body = perform(t, engine, http.MethodPost, proofPath, tokens["reviewer"], "gate-proof-reaccept", map[string]any{"status": "accepted", "expectedVersion": proofVersion, "reason": "tolerance verified on revised configuration"})
	if status != http.StatusOK {
		t.Fatalf("proof re-accept status = %d body=%s", status, body)
	}
	reaccepted := decodeData[struct {
		Version    uint `json:"version"`
		RunVersion uint `json:"runVersion"`
		Stale      bool `json:"stale"`
	}](t, body)
	if reaccepted.RunVersion != 2 || reaccepted.Stale {
		t.Fatalf("re-acceptance did not pin current batch version: %+v", reaccepted)
	}

	// Re-selecting the proof refreshes the decision snapshot; release succeeds.
	decisionUpdate := recordPayload("ignored", "重新选择校样")
	decisionUpdate["expectedVersion"] = decision.Version
	decisionUpdate["relatedCode"] = "PR-GATE-001"
	decisionUpdate["proofId"] = proof.ID
	status, body = perform(t, engine, http.MethodPut, "/api/release/"+uintString(decision.ID), tokens["operator"], "gate-decision-update", decisionUpdate)
	if status != http.StatusOK {
		t.Fatalf("update decision status = %d body=%s", status, body)
	}
	refreshed := decodeData[struct {
		Version         uint `json:"version"`
		ProofRunVersion uint `json:"proofRunVersion"`
	}](t, body)
	if refreshed.ProofRunVersion != 2 {
		t.Fatalf("decision snapshot not refreshed: %+v", refreshed)
	}
	status, body = perform(t, engine, http.MethodPost, releasePath, tokens["reviewer"], "gate-release", map[string]any{"status": "release", "expectedVersion": refreshed.Version, "reason": "fresh proof verified"})
	if status != http.StatusOK {
		t.Fatalf("release status = %d body=%s", status, body)
	}

	// Later proof edits must not rewrite the released decision's snapshot.
	proofUpdate := recordPayload("ignored", "接收后修改读数")
	proofUpdate["expectedVersion"] = reaccepted.Version
	proofUpdate["relatedCode"] = "PR-GATE-001"
	proofUpdate["metricValue"] = 9.9
	status, body = perform(t, engine, http.MethodPut, "/api/proofs/"+uintString(proof.ID), tokens["operator"], "gate-proof-edit", proofUpdate)
	if status != http.StatusOK {
		t.Fatalf("edit proof status = %d body=%s", status, body)
	}
	status, body = perform(t, engine, http.MethodGet, "/api/release/"+uintString(decision.ID), tokens["reviewer"], "gate-decision-read", nil)
	if status != http.StatusOK {
		t.Fatalf("read decision status = %d", status)
	}
	final := decodeData[struct {
		ProofValue float64 `json:"proofValue"`
		Revisions  []struct {
			Version         uint   `json:"version"`
			ProofCode       string `json:"proofCode"`
			ProofRunVersion uint   `json:"proofRunVersion"`
		} `json:"revisions"`
	}](t, body)
	if final.ProofValue != 2.1 || len(final.Revisions) != 3 || final.Revisions[0].ProofCode != "CP-GATE-001" || final.Revisions[0].ProofRunVersion != 2 {
		t.Fatalf("decision snapshot was rewritten by later proof edits: %+v", final)
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
