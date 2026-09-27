package ai

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/andres1m/impuls-goroda/pkg/svc"
)

//go:embed admin.html
var adminPage []byte

const maxSelectionBody = 4 << 10

// Admin serves the page that switches hosted models. It never changes the provider or the key.
type Admin struct {
	models *Models
	cfg    AdminConfig
	log    *zap.Logger
	mux    *http.ServeMux

	lis      net.Listener
	server   *http.Server
	stopOnce sync.Once
	stopErr  error
}

func NewAdmin(models *Models, cfg AdminConfig, log *zap.Logger) *Admin {
	a := &Admin{models: models, cfg: cfg, log: log, mux: http.NewServeMux()}
	a.mux.HandleFunc("GET /{$}", a.page)
	a.mux.HandleFunc("GET /api/state", a.state)
	a.mux.HandleFunc("GET /api/models", a.list)
	a.mux.HandleFunc("PUT /api/selection", a.selection)
	return a
}

func (a *Admin) Handler() http.Handler { return a.mux }

func (a *Admin) Name() string { return "ai-admin" }

func (a *Admin) DependsOn() []string { return []string{"logger"} }

func (a *Admin) Init(ctx context.Context) error {
	if a.server != nil {
		return errors.New("ai admin already initialized")
	}
	host := a.cfg.Host
	if host == "" {
		host = "127.0.0.1"
	}
	lis, err := (&net.ListenConfig{}).Listen(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(a.cfg.Port)))
	if err != nil {
		return fmt.Errorf("ai admin listen: %w", err)
	}
	a.lis = lis
	a.server = &http.Server{Handler: a.mux, ReadHeaderTimeout: 5 * time.Second}
	a.log.Info("ai admin listening", zap.String("addr", lis.Addr().String()))
	return nil
}

func (a *Admin) HealthCheck(context.Context) error {
	if a.server == nil {
		return errors.New("ai admin is not initialized")
	}
	return nil
}

func (a *Admin) Run(context.Context) error {
	if a.server == nil {
		return errors.New("ai admin is not initialized")
	}
	if err := a.server.Serve(a.lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("ai admin failed: %w", err)
	}
	return nil
}

func (a *Admin) Stop(ctx context.Context) error {
	if a.server == nil {
		return nil
	}
	a.stopOnce.Do(func() {
		a.stopErr = a.server.Shutdown(ctx)
		// Shutdown closes only listeners Serve adopted; a rollback before Run must free the port too.
		if err := a.lis.Close(); err != nil && !errors.Is(err, net.ErrClosed) && a.stopErr == nil {
			a.stopErr = err
		}
	})
	return a.stopErr
}

// Addr is known only after Init.
func (a *Admin) Addr() net.Addr {
	if a.lis == nil {
		return nil
	}
	return a.lis.Addr()
}

type spaceView struct {
	Key     string `json:"key"`
	Version string `json:"version"`
}

type kindView struct {
	Provider string     `json:"provider"`
	Model    string     `json:"model"`
	Space    *spaceView `json:"space,omitempty"`
}

type stateView struct {
	Embedding kindView `json:"embedding"`
	Text      kindView `json:"text"`
	APIKeySet bool     `json:"api_key_set"`
}

func (a *Admin) page(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(adminPage)
}

func (a *Admin) state(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.view())
}

func (a *Admin) view() stateView {
	space := a.models.Embedder().Space()
	return stateView{
		Embedding: kindView{
			Provider: a.models.Provider(KindEmbedding), Model: a.models.Selected(KindEmbedding),
			Space: &spaceView{Key: space.Key, Version: space.Version},
		},
		Text:      kindView{Provider: a.models.Provider(KindText), Model: a.models.Selected(KindText)},
		APIKeySet: a.models.cfg.OpenRouter.APIKey != "",
	}
}

func (a *Admin) list(w http.ResponseWriter, r *http.Request) {
	models, err := a.models.Available(r.Context(), Kind(r.URL.Query().Get("kind")))
	switch {
	case errors.Is(err, ErrUnknownKind):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		a.log.Warn("ai admin cannot list models", zap.Error(err))
		writeError(w, http.StatusBadGateway, err)
	default:
		if models == nil {
			models = []ModelInfo{}
		}
		writeJSON(w, http.StatusOK, models)
	}
}

func (a *Admin) selection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind  Kind   `json:"kind"`
		Model string `json:"model"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSelectionBody)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid selection request"))
		return
	}
	if req.Kind != KindEmbedding && req.Kind != KindText {
		writeError(w, http.StatusBadRequest, ErrUnknownKind)
		return
	}
	if req.Model == "" {
		writeError(w, http.StatusBadRequest, errors.New("model is required"))
		return
	}
	if err := a.models.Select(r.Context(), req.Kind, req.Model); err != nil {
		a.log.Warn("ai admin refused a model", zap.String("kind", string(req.Kind)), zap.String("model", req.Model), zap.Error(err))
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	a.log.Info("ai model selected", zap.String("kind", string(req.Kind)), zap.String("model", req.Model))
	writeJSON(w, http.StatusOK, a.view())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

var _ svc.Service = (*Admin)(nil)
