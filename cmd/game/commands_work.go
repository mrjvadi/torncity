package main

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// bindWork maps the work and study commands to their handlers. bind merges
// it into the one table bindAll checks against the subscriptions.
func (h phaseHandlers) bindWork() map[string]commandFunc {
	return map[string]commandFunc{
		"job.status": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.jobs.Status(ctx, env.Metadata)
		},
		"job.list": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.PageRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.jobs.List(ctx, env.Metadata, req)
		},
		"job.view": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.JobRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.jobs.View(ctx, env.Metadata, req)
		},
		"job.apply": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.JobRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.jobs.Apply(ctx, env.Metadata, req)
		},
		"job.work": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.jobs.Work(ctx, env.Metadata)
		},
		"job.promote": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.jobs.Promote(ctx, env.Metadata)
		},
		"job.quit": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.QuitRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.jobs.Quit(ctx, env.Metadata, req)
		},

		"education.list": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.PageRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.education.List(ctx, env.Metadata, req)
		},
		"education.view": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.CourseRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.education.View(ctx, env.Metadata, req)
		},
		"education.enroll": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.CourseRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.education.Enroll(ctx, env.Metadata, req)
		},
		// The scheduler's dispatch payload, as for travel.arrive.
		"education.complete": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.CompleteCourseRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.education.Complete(ctx, env.Metadata, req)
		},
	}
}

// newWorkHandlers builds the work and study handlers. Both read content from
// the registry per request, so a content reload reaches them as a pointer
// swap.
func newWorkHandlers(
	uow application.UnitOfWork,
	msgs handlers.Translator,
	registry *content.Registry,
	cities application.CityRepository,
	policy application.PolicyReader,
	idempotencyTTL time.Duration,
) (*handlers.JobsHandler, *handlers.EducationHandler) {
	jobs := handlers.NewJobsHandler(uow, uuidGenerator{}, msgs, registry, cities, policy,
		handlers.DefaultPageSize, idempotencyTTL, nil)
	education := handlers.NewEducationHandler(uow, uuidGenerator{}, msgs, registry, cities,
		handlers.DefaultPageSize, idempotencyTTL, nil)
	return jobs, education
}
