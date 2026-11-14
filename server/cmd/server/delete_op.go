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

// deleteTaskRequest is the subset of the delete request the op needs to validate.
type deleteTaskRequest struct {
  Paths []string `json:"paths"`
}

// registerDeleteOp wires the delete operation into the task service. The roots
// factory is captured so validation stays confined to the allowed folders, and
// the runner closes over the resolved plan. Adding a new operation follows the
// same shape: implement the work and Register it.
func registerDeleteOp(svc *service.Service, roots func() *fs.Roots) {
  svc.Register(service.DeleteOperation, service.Op{
    Prepare: func(body []byte) (any, []byte, error) {
      var req deleteTaskRequest
      if err := json.Unmarshal(body, &req); err != nil {
        return nil, nil, fmt.Errorf("invalid delete request: %w", err)
      }
      plan, err := ops.DeleteResolve(roots(), req.Paths)
      if err != nil {
        return nil, nil, err
      }
      meta, _ := json.Marshal(plan)
      return plan, meta, nil
    },
    Run: func(ctx context.Context, spec any, prog *task.Progression) error {
      return ops.DeleteRun(ctx, spec.(*ops.DeletePlan), prog)
    },
  })
}
