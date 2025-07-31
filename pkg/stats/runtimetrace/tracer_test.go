package runtimetrace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	runtimeTrace "runtime/trace"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/kitex/pkg/logid"

	"github.com/cloudwego/kitex/pkg/consts"
	"golang.org/x/exp/trace"
)

/*
Go Runtime Trace 解析器实现

功能特性：
1. 解析 runtime/trace 采集的 trace 文件
2. 分析 goroutine 的父子关系
3. 跟踪 goroutine 的生命周期（创建时间、结束时间、持续时间）
4. 提供树形结构显示 goroutine 关系
5. 统计分析功能（状态分布、平均执行时间、树深度等）
6. 支持调用栈信息记录
7. 容错处理，支持不完整的 trace 文件

使用方法：
1. 使用 StartTrace() 开始 trace 采集
2. 执行你的 goroutine 代码
3. 使用 EndTrace() 结束 trace 采集
4. 使用 TraceParser 解析生成的 trace 文件

示例：
	parser := NewTraceParser()
	err := parser.ParseTraceFile("trace_file")
	if err != nil {
		log.Fatal(err)
	}

	// 打印统计信息
	stats := parser.GetStatistics()
	fmt.Printf("Total goroutines: %v\n", stats["total_goroutines"])

	// 打印 goroutine 树
	parser.PrintGoroutineTree()

注意事项：
- 当前实现使用启发式算法推断父子关系，在复杂场景下可能不完全准确
- 支持 golang.org/x/exp/trace 包格式
- 对于不兼容的 trace 格式会自动降级到基础解析模式
*/

func TestExecutionTracer(t *testing.T) {
	StartTrace()
	tracer := &executionTracer{}
	for j := 0; j < 5; j++ {
		ctx := context.WithValue(context.Background(), consts.CtxKeyLogID, logid.DefaultLogIDGenerator(context.Background()))
		ctx = tracer.Start(ctx)
		wg := sync.WaitGroup{}
		for i := 0; i < 1; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				id := getLogid(ctx)
				runtimeTrace.WithRegion(ctx, id, testFunc)
			}()
		}
		wg.Wait()
		tracer.Finish(ctx)
	}
	EndTrace()
	time.Sleep(time.Second)
	// parseTrace()
}

func TestParseTrace(t *testing.T) {
	parseTrace()
}

func TestGoroutineTraceAnalysis(t *testing.T) {
	// 创建一个更复杂的测试场景来验证父子关系分析
	StartTrace()

	// 启动根 goroutine 任务
	tracer := &executionTracer{}
	rootCtx := context.WithValue(context.Background(), consts.CtxKeyLogID, "root-task")
	rootCtx = tracer.Start(rootCtx)

	// 创建多层 goroutine 嵌套
	wg := sync.WaitGroup{}

	// 第一层子 goroutine
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			childCtx := context.WithValue(context.Background(), consts.CtxKeyLogID, fmt.Sprintf("child-%d", id))
			childCtx = tracer.Start(childCtx)

			// 第二层子 goroutine
			subWg := sync.WaitGroup{}
			for j := 0; j < 2; j++ {
				subWg.Add(1)
				go func(childId, subId int) {
					defer subWg.Done()

					grandChildCtx := context.WithValue(context.Background(), consts.CtxKeyLogID,
						fmt.Sprintf("grandchild-%d-%d", childId, subId))
					grandChildCtx = tracer.Start(grandChildCtx)

					// 执行一些工作
					runtimeTrace.WithRegion(grandChildCtx, fmt.Sprintf("work-%d-%d", childId, subId), func() {
						time.Sleep(10 * time.Millisecond)
						testFunc()
					})

					tracer.Finish(grandChildCtx)
				}(id, j)
			}
			subWg.Wait()

			tracer.Finish(childCtx)
		}(i)
	}

	wg.Wait()
	tracer.Finish(rootCtx)

	EndTrace()

	// 等待 trace 写入完成
	time.Sleep(100 * time.Millisecond)
}

func parseTrace() {
	parser := NewTraceParser()

	err := parser.ParseTraceFile("tracing_test")
	if err != nil {
		fmt.Printf("Error parsing trace with advanced parser: %v\n", err)
		fmt.Println("Falling back to basic trace analysis...")
		parseTraceBasic()
		return
	}

	// 打印统计信息
	stats := parser.GetStatistics()
	fmt.Println("Trace Analysis Results:")
	fmt.Println("======================")
	for key, value := range stats {
		fmt.Printf("%s: %v\n", key, value)
	}
	fmt.Println()

	// 如果有goroutine数据，打印详细信息
	if len(parser.GetAllGoroutines()) > 0 {
		// 打印 goroutine 树
		parser.PrintGoroutineTree()

		// 打印详细的 goroutine 信息
		fmt.Println("\nDetailed Goroutine Information:")
		fmt.Println("===============================")
		for goid, info := range parser.GetAllGoroutines() {
			fmt.Printf("Goroutine %d:\n", goid)
			fmt.Printf("  Parent: %d\n", info.ParentID)
			fmt.Printf("  Created: %v\n", info.Created)
			if info.Finished != 0 {
				fmt.Printf("  Finished: %v\n", info.Finished)
				durationNs := int64(info.Finished - info.Created)
				fmt.Printf("  Duration: %v\n", time.Duration(durationNs))
			} else {
				fmt.Printf("  Status: Still running\n")
			}
			fmt.Printf("  Children: %v\n", info.Children)
			fmt.Printf("  Final State: %v\n", info.State)
			fmt.Println()
		}
	} else {
		fmt.Println("No goroutine information extracted from trace.")
	}
}

// parseTraceBasic 提供基础的trace解析功能作为备用方案
func parseTraceBasic() {
	f, err := os.Open("tracing_test")
	if err != nil {
		fmt.Printf("Failed to open trace file: %v\n", err)
		return
	}
	defer f.Close()

	reader, err := trace.NewReader(f)
	if err != nil {
		fmt.Printf("Failed to create trace reader: %v\n", err)
		return
	}

	eventCount := 0
	goroutineEvents := 0
	stateTransitions := 0

	fmt.Println("Basic Trace Analysis:")
	fmt.Println("====================")

	for {
		ev, err := reader.ReadEvent()
		if err != nil {
			if err == io.EOF {
				break
			}
			fmt.Printf("Stopped reading at event %d due to error: %v\n", eventCount, err)
			break
		}

		eventCount++

		switch ev.Kind() {
		case trace.EventStateTransition:
			stateTransitions++
			st := ev.StateTransition()
			if st.Resource.Kind == trace.ResourceGoroutine {
				goroutineEvents++
				from, _ := st.Goroutine()
				if from == trace.GoNotExist {
					fmt.Printf("Goroutine %d created at time %d\n", st.Resource.Goroutine(), ev.Time())
				}
			}
		}

		// 限制处理的事件数量以避免过长输出
		if eventCount >= 1000 {
			fmt.Printf("Processed first 1000 events...\n")
			break
		}
	}

	fmt.Printf("Total events processed: %d\n", eventCount)
	fmt.Printf("State transitions: %d\n", stateTransitions)
	fmt.Printf("Goroutine-related events: %d\n", goroutineEvents)
}

func testFunc() {
	fmt.Println("testFunc")
	ts := testStruct{
		ID:      1,
		Name:    "Example",
		Tags:    []string{"alpha", "beta"},
		Enabled: true,
	}
	for i := 0; i < 10; i++ {
		b, err := json.Marshal(ts)
		if err != nil {
			fmt.Printf("marshal error=%v\n", err)
			continue
		}
		res := testStruct{}
		err = json.Unmarshal(b, &res)
		if err != nil {
			fmt.Printf("unmarshal error=%v\n", err)
		}
	}
}

type testStruct struct {
	ID      int      `json:"id"`
	Name    string   `json:"name"`
	Tags    []string `json:"tags"`
	Enabled bool     `json:"enabled"`
	Meta    struct {
		Author  string `json:"author"`
		Version int    `json:"version"`
	} `json:"meta"`
}
