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

// moveTaskRequest is the subset of the move request the op needs to validate.
type moveTaskRequest struct {
	Paths []string `json:"paths"`
	Dest  string   `json:"dest"`
}

// registerMoveOp wires the move operation into the task service. The roots
// factory is captured so validation stays confined to the allowed folders, and
// the runner closes over the resolved plan. Move overwrites an existing same-name
// item at the destination, so (unlike copy) there is no conflict field here.
func registerMoveOp(svc *service.Service, roots func() *fs.Roots, cfg *config.Config) {
	svc.Register(service.MoveOperation, service.Op{
		Prepare: func(body []byte) (any, []byte, error) {
			var req moveTaskRequest
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, nil, fmt.Errorf("invalid move request: %w", err)
			}
			plan, err := ops.MoveResolve(roots(), req.Paths, req.Dest)
			if err != nil {
				return nil, nil, err
			}
			meta, _ := json.Marshal(plan)
			return plan, meta, nil
		},
		Run: func(ctx context.Context, spec any, prog *task.Progression) error {
			return ops.MoveRun(ctx, spec.(*ops.MovePlan), prog, &ops.MoveConfig{BufferSize: cfg.MoveBufferSize})
		},
	})
}
