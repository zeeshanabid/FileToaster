package main

import (
	"context"
	"encoding/json"
	"fmt"

	"filetoaster/internal/fs"
	"filetoaster/internal/fs/ops"
	"filetoaster/internal/service"
	"filetoaster/internal/task"
)

// registerSearchOp wires the recursive search operation into the task service.
// The roots factory is captured so the requested search folder stays confined to
// the allowed roots, and the runner closes over the resolved plan. Search reuses
// the task engine (worker pool, cancellation, SSE progress) and additionally
// streams each match over SSE as a "search-result" event.
func registerSearchOp(svc *service.Service, roots func() *fs.Roots) {
	svc.Register(service.SearchOperation, service.Op{
		Prepare: func(body []byte) (any, []byte, error) {
			var req ops.SearchRequest
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, nil, fmt.Errorf("invalid search request: %w", err)
			}
			plan, err := ops.SearchResolve(roots(), req)
			if err != nil {
				return nil, nil, err
			}
			meta, _ := json.Marshal(plan)
			return plan, meta, nil
		},
		Run: func(ctx context.Context, spec any, prog *task.Progression) error {
			return ops.SearchRun(ctx, spec.(*ops.SearchPlan), prog)
		},
	})
}
