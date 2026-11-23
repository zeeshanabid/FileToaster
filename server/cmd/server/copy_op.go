package main

import (
	"context"
	"encoding/json"
	"fmt"

	"filetoaster/internal/config"
	"filetoaster/internal/fs"
	"filetoaster/internal/fs/ops"
	"filetoaster/internal/service"
	"filetoaster/internal/task"
)

// copyTaskRequest is the subset of the copy request the op needs to validate.
type copyTaskRequest struct {
	Paths []string `json:"paths"`
	Dest  string   `json:"dest"`
}

// registerCopyOp wires the copy operation into the task service. The roots
// factory is captured so validation stays confined to the allowed folders, and
// the runner closes over the resolved plan. Adding a new operation follows the
// same shape: implement the work and Register it.
func registerCopyOp(svc *service.Service, roots func() *fs.Roots, cfg *config.Config) {
	svc.Register(service.CopyOperation, service.Op{
		Prepare: func(body []byte) (any, []byte, error) {
			var req copyTaskRequest
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, nil, fmt.Errorf("invalid copy request: %w", err)
			}
			plan, err := ops.Resolve(roots(), req.Paths, req.Dest)
			if err != nil {
				return nil, nil, err
			}
			meta, _ := json.Marshal(plan)
			return plan, meta, nil
		},
		Run: func(ctx context.Context, spec any, prog *task.Progression) error {
			return ops.Run(ctx, spec.(*ops.Plan), prog, &ops.CopyConfig{BufferSize: cfg.CopyBufferSize})
		},
	})
}
