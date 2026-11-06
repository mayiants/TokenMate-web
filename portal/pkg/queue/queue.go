package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type Job struct {
	Type       string          `json:"type"`
	Payload    json.RawMessage `json:"payload"`
	RetryCount int             `json:"retry_count,omitempty"`
}

type Handler func(payload json.RawMessage) error

type JobQueue struct {
	redis   *redis.Client
	logger  *zap.Logger
	handler map[string]Handler
}

// maxRetries is the number of times a failing job is re-enqueued before being
// moved to the dead-letter queue. A retry counter is carried in the Job payload
// so it survives Redis round-trips.
const maxRetries = 3

func NewJobQueue(rdb *redis.Client, logger *zap.Logger) *JobQueue {
	return &JobQueue{
		redis:   rdb,
		logger:  logger,
		handler: map[string]Handler{},
	}
}

func (q *JobQueue) Register(jobType string, handler Handler) {
	q.handler[jobType] = handler
}

func (q *JobQueue) Enqueue(ctx context.Context, jobType string, payload interface{}) error {
	if q.redis == nil {
		q.logger.Warn("Redis not available, job not enqueued", zap.String("type", jobType))
		return fmt.Errorf("redis not available")
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	job := Job{Type: jobType, Payload: data}
	raw, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}

	key := fmt.Sprintf("queue:%s", jobType)
	if err := q.redis.LPush(ctx, key, raw).Err(); err != nil {
		return fmt.Errorf("lpush: %w", err)
	}

	q.logger.Debug("Job enqueued", zap.String("type", jobType))
	return nil
}

// requeue pushes a failed job back onto its queue with an incremented retry
// counter. After maxRetries it is moved to the dead-letter queue so it is not
// permanently lost and can be inspected / replayed.
func (q *JobQueue) requeue(ctx context.Context, job Job) {
	if job.RetryCount >= maxRetries {
		dlqKey := fmt.Sprintf("queue:%s:dlq", job.Type)
		raw, err := json.Marshal(job)
		if err != nil {
			q.logger.Error("Failed to marshal job for DLQ",
				zap.String("type", job.Type),
				zap.Int("retry_count", job.RetryCount),
				zap.Error(err))
			return
		}
		if err := q.redis.LPush(ctx, dlqKey, raw).Err(); err != nil {
			q.logger.Error("Failed to push job to dead-letter queue",
				zap.String("type", job.Type),
				zap.String("dlq_key", dlqKey),
				zap.Error(err))
			return
		}
		q.logger.Error("Job moved to dead-letter queue after exhausting retries",
			zap.String("type", job.Type),
			zap.Int("retry_count", job.RetryCount),
			zap.String("dlq_key", dlqKey))
		return
	}

	job.RetryCount++
	raw, err := json.Marshal(job)
	if err != nil {
		q.logger.Error("Failed to marshal job for retry",
			zap.String("type", job.Type),
			zap.Int("retry_count", job.RetryCount),
			zap.Error(err))
		return
	}
	key := fmt.Sprintf("queue:%s", job.Type)
	if err := q.redis.LPush(ctx, key, raw).Err(); err != nil {
		q.logger.Error("Failed to re-enqueue failed job",
			zap.String("type", job.Type),
			zap.Int("retry_count", job.RetryCount),
			zap.Error(err))
		return
	}
	q.logger.Warn("Job handler failed, re-enqueued for retry",
		zap.String("type", job.Type),
		zap.Int("retry_count", job.RetryCount),
		zap.Int("max_retries", maxRetries))
}

func (q *JobQueue) StartWorker(ctx context.Context, jobType string) {
	handler, ok := q.handler[jobType]
	if !ok {
		q.logger.Error("No handler registered", zap.String("type", jobType))
		return
	}

	key := fmt.Sprintf("queue:%s", jobType)
	q.logger.Info("Job worker started", zap.String("type", jobType))

	go func() {
		for {
			select {
			case <-ctx.Done():
				q.logger.Info("Job worker stopped", zap.String("type", jobType))
				return
			default:
			}

			result, err := q.redis.BRPop(ctx, 5*time.Second, key).Result()
			if err != nil {
				if err == redis.Nil || ctx.Err() != nil {
					continue
				}
				q.logger.Error("BRPop error", zap.Error(err), zap.String("type", jobType))
				time.Sleep(time.Second)
				continue
			}

			if len(result) < 2 {
				continue
			}

			var job Job
			if err := json.Unmarshal([]byte(result[1]), &job); err != nil {
				q.logger.Error("Unmarshal job error", zap.Error(err))
				continue
			}

			if err := handler(job.Payload); err != nil {
				q.logger.Error("Job handler error",
					zap.String("type", jobType),
					zap.Int("retry_count", job.RetryCount),
					zap.Error(err))
				// Re-enqueue with a retry counter; after maxRetries the job
				// is moved to the dead-letter queue instead of being lost.
				q.requeue(ctx, job)
			} else {
				q.logger.Debug("Job processed", zap.String("type", jobType))
			}
		}
	}()
}
