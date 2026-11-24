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

// decompressTaskRequest is the subset of the decompress request the op needs to
// validate: the single selected path, the extraction destination ("" to extract
// here or the archive stem to extract into a sibling folder), and the current
// window path so the client can reload the correct directory after extraction.
type decompressTaskRequest struct {
	Paths      []string `json:"paths"`
	Into       string   `json:"into"`
	WindowPath string   `json:"windowPath"`
}

// registerDecompressOp wires the decompress operation into the task service. The
// roots factory is captured so validation stays confined to the allowed folders,
// and the runner closes over the resolved plan. Adding a new operation follows
// the same shape: implement the work and Register it.
func registerDecompressOp(svc *service.Service, roots func() *fs.Roots, cfg *config.Config) {
	svc.Register(service.DecompressOperation, service.Op{
		Prepare: func(body []byte) (any, []byte, error) {
			var req decompressTaskRequest
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, nil, fmt.Errorf("invalid decompress request: %w", err)
			}
			plan, err := ops.DecompressResolve(roots(), req.Paths, req.Into)
			if err != nil {
				return nil, nil, err
			}
			meta, _ := json.Marshal(ops.DecompressMeta{
				Format:     plan.Format,
				Archive:    plan.Archive,
				ExtractDir: plan.ExtractDir,
				Into:       req.Into,
				WindowPath: req.WindowPath,
			})
			return plan, meta, nil
		},
		Run: func(ctx context.Context, spec any, prog *task.Progression) error {
			return ops.DecompressRun(ctx, spec.(*ops.DecompressPlan), prog, &ops.DecompressConfig{BufferSize: cfg.DecompressBufferSizeBytes()})
		},
	})
}
