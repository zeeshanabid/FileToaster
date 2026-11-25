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

// truncateTaskRequest is the subset of the truncate request the op needs to validate.
type truncateTaskRequest struct {
	Paths []string `json:"paths"`
	Size  int64    `json:"size"`
}

// registerTruncateOp wires the truncate operation into the task service. The roots
// factory is captured so validation stays confined to the allowed folders, and
// the runner closes over the resolved plan. Truncate only works on files, not directories.
func registerTruncateOp(svc *service.Service, roots func() *fs.Roots, cfg *config.Config) {
	svc.Register(service.TruncateOperation, service.Op{
		Prepare: func(body []byte) (any, []byte, error) {
			var req truncateTaskRequest
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, nil, fmt.Errorf("invalid truncate request: %w", err)
			}
			plan, err := ops.TruncateResolve(roots(), req.Paths, req.Size)
			if err != nil {
				return nil, nil, err
			}
			meta, _ := json.Marshal(plan)
			return plan, meta, nil
		},
		Run: func(ctx context.Context, spec any, prog *task.Progression) error {
			return ops.TruncateRun(ctx, spec.(*ops.TruncatePlan), prog, &ops.TruncateConfig{BufferSize: cfg.CopyBufferSizeBytes()})
		},
	})
}
