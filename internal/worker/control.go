package worker

import (
	"context"
	"sync"
)

type Controller struct {
	mu      sync.Mutex
	cancels map[int64]context.CancelFunc
}

func NewController() *Controller {
	return &Controller{cancels: make(map[int64]context.CancelFunc)}
}

func (c *Controller) Register(jobID int64, cancel context.CancelFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancels[jobID] = cancel
}

func (c *Controller) Unregister(jobID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cancels, jobID)
}

func (c *Controller) Cancel(jobID int64) bool {
	c.mu.Lock()
	cancel, ok := c.cancels[jobID]
	c.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}
