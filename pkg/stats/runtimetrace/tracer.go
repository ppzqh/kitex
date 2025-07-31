package runtimetrace

import (
	"context"
	runtimeTrace "runtime/trace"

	"github.com/cloudwego/kitex/pkg/consts"
	"github.com/cloudwego/kitex/pkg/stats"
	"github.com/cloudwego/kitex/pkg/utils/kitexutil"
)

const (
	logKey  = "k-logid"
	spanKey = "execution-span"
)

var (
	_           stats.Tracer = &executionTracer{}
	traceWriter *fileWriter
)

func init() {
	tw, err := newFileWriter("tracing_test")
	if err != nil {
		panic(err)
	}
	traceWriter = tw
}

func StartTrace() {
	runtimeTrace.Start(traceWriter)
}

func EndTrace() {
	runtimeTrace.Stop()
}

type span struct {
	id          string
	taskEndFunc func()
}

type executionTracer struct{}

func (e *executionTracer) Start(ctx context.Context) context.Context {
	if !runtimeTrace.IsEnabled() {
		return ctx
	}

	var (
		task     *runtimeTrace.Task
		taskName string = "-"
	)
	if method, ok := kitexutil.GetMethod(ctx); ok {
		taskName = method
	}
	// new task
	ctx, task = runtimeTrace.NewTask(ctx, taskName)
	endFunc := task.End
	// inject logid/traceid
	id := getLogid(ctx)
	runtimeTrace.Log(ctx, logKey, id)

	sp := &span{id: id, taskEndFunc: endFunc}
	return context.WithValue(ctx, spanKey, sp)
}

func (e *executionTracer) Finish(ctx context.Context) {
	if sp, ok := ctx.Value(spanKey).(*span); ok {
		sp.taskEndFunc()
	}
}

func getLogid(ctx context.Context) string {
	v, ok := ctx.Value(consts.CtxKeyLogID).(string)
	if !ok {
		return ""
	}
	return v
}
