package fizeau

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/serviceimpl"
)

type executeRouteResolver interface {
	resolveExecuteRoute(ServiceExecuteRequest) (*RouteDecision, error)
	resolveExecuteRouteContext(context.Context, ServiceExecuteRequest) (*RouteDecision, error)
}

type executeSessionLogOpener interface {
	openSessionLog(ServiceExecuteRequest, RouteDecision, string) *serviceSessionLog
}

type executeEventFanout interface {
	OpenSession(string)
	BroadcastEvent(string, ServiceEvent)
	CloseSession(string, ServiceEvent)
}

type executeRunnerInvoker interface {
	dispatchExecuteRun(context.Context, executeRunContext)
}

type executeRunContext struct {
	req      ServiceExecuteRequest
	decision RouteDecision
	meta     map[string]string
	out      chan<- ServiceEvent
	seq      *atomic.Int64
	start    time.Time
	sl       *serviceSessionLog
	session  string
}

func (s *service) executeRouteResolver() executeRouteResolver {
	return s
}

func (s *service) executeSessionLogOpener() executeSessionLogOpener {
	return s
}

func (s *service) executeEventFanout() executeEventFanout {
	return s.hub
}

func (s *service) executeRunnerInvoker() executeRunnerInvoker {
	return s
}

func (s *service) dispatchExecuteRun(ctx context.Context, run executeRunContext) {
	decision := serviceimpl.ExecuteRunnerDecision{
		Harness:  run.decision.Harness,
		Provider: run.decision.Provider,
		Model:    run.decision.Model,
	}
	serviceimpl.DispatchExecuteRun(ctx, serviceimpl.ExecuteDispatchRequest{
		Decision: decision,
		Started:  run.start,
	}, serviceimpl.ExecuteDispatchCallbacks{
		RunNative: func(ctx context.Context) {
			s.runNative(ctx, run.req, run.decision, run.meta, run.out, run.seq, run.start, run.sl, run.session)
		},
		RunSubprocess: func(ctx context.Context, runner harnesses.Harness) {
			if s.subprocessDispatchObserver != nil {
				s.subprocessDispatchObserver(runner)
			}
			s.runSubprocess(ctx, run.req, run.decision, run.meta, run.out, run.seq, run.start, run.sl, run.session, runner)
		},
		RunVirtual: func(ctx context.Context) {
			s.runVirtual(ctx, run.req, run.decision, run.meta, run.out, run.seq, run.start, run.sl, run.session)
		},
		RunScript: func(ctx context.Context) {
			s.runScript(ctx, run.req, run.decision, run.meta, run.out, run.seq, run.start, run.sl, run.session)
		},
		IsHTTPProvider: func(harness string) bool {
			cfg, ok := s.registry.Get(harness)
			return ok && cfg.IsHTTPProvider
		},
		Finalize: func(final harnesses.FinalData) {
			s.recordRouteAttemptFromFinal(final)
			finalizeAndEmit(run.out, run.seq, run.meta, run.req, run.sl, final, serviceimpl.TerminalOriginSpawn, ctx.Err())
		},
	})
}
