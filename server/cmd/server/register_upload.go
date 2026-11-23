package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"filetoaster/internal/config"
	"filetoaster/internal/fs"
	"filetoaster/internal/service"
	"filetoaster/internal/task"
	"filetoaster/internal/upload"
)

// finalizeUploadRequest is the body that creates the tracked finalize op from a
// completed upload session: the staging dir, the (already confined) destination,
// the file count (for the task label), and the phase flag.
type finalizeUploadRequest struct {
	Staging string `json:"staging"`
	Dest    string `json:"dest"`
	Files   int    `json:"files"`
	Phase   string `json:"phase"`
}

// registerUploadOp wires the upload finalize operation into the task service.
// The roots factory confines the destination to the allowed folders; the runner
// copies the staged tree into it (see internal/upload.Finalize).
func registerUploadOp(svc *service.Service, roots func() *fs.Roots, cfg *config.Config) {
	svc.Register(service.UploadOperation, service.Op{
		Prepare: func(body []byte) (any, []byte, error) {
			var req finalizeUploadRequest
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, nil, fmt.Errorf("invalid upload finalize request: %w", err)
			}
			if strings.TrimSpace(req.Staging) == "" || strings.TrimSpace(req.Dest) == "" {
				return nil, nil, fmt.Errorf("upload finalize requires 'staging' and 'dest'")
			}
			if _, err := roots().Resolve(req.Dest); err != nil {
				return nil, nil, err
			}
			meta, _ := json.Marshal(upload.FinalizeMeta{
				Staging: req.Staging,
				Dest:    req.Dest,
				Phase:   req.Phase,
				Files:   req.Files,
			})
			return req, meta, nil
		},
		Run: func(ctx context.Context, spec any, prog *task.Progression) error {
			req := spec.(finalizeUploadRequest)
			return upload.Finalize(ctx, req.Staging, req.Dest, prog, &upload.FinalizeConfig{BufferSize: cfg.FinalizeBufferSize})
		},
	})
}
