package http

import "net/http"

func (h *Handlers) healthz(w http.ResponseWriter, r *http.Request) error {
	if err := h.Svc.Healthy(r.Context()); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
	return nil
}
