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

// compressTaskRequest is the subset of the compress request the op needs to
// validate: the selected paths and the chosen archive format (zip, targz, tar).
type compressTaskRequest struct {
	Paths  []string `json:"paths"`
	Format string   `json:"format"`
}

// registerCompressOp wires the compress operation into the task service. The
// roots factory is captured so validation stays confined to the allowed folders,
// and the runner closes over the resolved plan. Adding a new operation follows
// the same shape: implement the work and Register it.
func registerCompressOp(svc *service.Service, roots func() *fs.Roots, cfg *config.Config) {
	svc.Register(service.CompressOperation, service.Op{
		Prepare: func(body []byte) (any, []byte, error) {
			var req compressTaskRequest
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, nil, fmt.Errorf("invalid compress request: %w", err)
			}
			plan, err := ops.CompressResolve(roots(), req.Paths, req.Format)
			if err != nil {
				return nil, nil, err
			}
			meta, _ := json.Marshal(ops.CompressMeta{
				Format: plan.Format,
				Dest:   plan.Dest,
				Source: len(plan.Sources),
			})
			return plan, meta, nil
		},
		Run: func(ctx context.Context, spec any, prog *task.Progression) error {
			return ops.CompressRun(ctx, spec.(*ops.CompressPlan), prog, &ops.CompressConfig{BufferSize: cfg.CompressBufferSizeBytes()})
		},
	})
}
