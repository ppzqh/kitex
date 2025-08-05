package runtimetrace

import (
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/exp/trace"
)

// GoroutineInfo 记录 goroutine 的详细信息
type GoroutineInfo struct {
	ID       trace.GoID
	ParentID trace.GoID
	Created  trace.Time
	Finished trace.Time
	State    trace.GoState
	Stack    []trace.StackFrame
	Children []trace.GoID
	TraceID  string // 关联的 trace.Log() 注入的 ID
}

// TraceParser 用于解析 runtime trace 文件
type TraceParser struct {
	goroutines    map[trace.GoID]*GoroutineInfo
	relationships map[trace.GoID][]trace.GoID // parent -> children
	events        []trace.Event
	goroutineTraceIDs map[trace.GoID]string    // goroutine -> trace ID mapping
	loggedTraceIDs    map[trace.GoID]string    // 每个goroutine最后记录的trace.Log() ID
}

// NewTraceParser 创建新的 trace 解析器
func NewTraceParser() *TraceParser {
	return &TraceParser{
		goroutines:        make(map[trace.GoID]*GoroutineInfo),
		relationships:     make(map[trace.GoID][]trace.GoID),
		events:            make([]trace.Event, 0),
		goroutineTraceIDs: make(map[trace.GoID]string),
		loggedTraceIDs:    make(map[trace.GoID]string),
	}
}

// ParseTraceFile 解析指定的 trace 文件
func (tp *TraceParser) ParseTraceFile(filename string) error {
	f, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open trace file: %w", err)
	}
	defer f.Close()

	reader, err := trace.NewReader(f)
	if err != nil {
		return fmt.Errorf("failed to create trace reader: %w", err)
	}

	eventCount := 0
	for {
		ev, err := reader.ReadEvent()
		if err != nil {
			if err == io.EOF {
				break
			}
			// 如果遇到不支持的事件格式，记录错误但继续处理
			fmt.Printf("Warning: failed to read event at position %d: %v\n", eventCount, err)

			// 如果连续失败太多次，就停止解析
			if eventCount == 0 {
				return fmt.Errorf("failed to read any events: %w", err)
			}
			break
		}

		tp.events = append(tp.events, ev)
		tp.processEvent(ev)
		eventCount++
	}

	fmt.Printf("Successfully processed %d trace events\n", eventCount)
	tp.buildRelationships()
	return nil
}

// processEvent 处理单个 trace 事件
func (tp *TraceParser) processEvent(ev trace.Event) {
	switch ev.Kind() {
	case trace.EventStateTransition:
		tp.processStateTransition(ev)
	case trace.EventRangeBegin, trace.EventRangeActive, trace.EventRangeEnd:
		tp.processRangeEvent(ev)
	case trace.EventTaskBegin, trace.EventTaskEnd:
		tp.processTaskEvent(ev)
	case trace.EventLog:
		tp.processLogEvent(ev)
	}
}

// processStateTransition 处理状态转换事件
func (tp *TraceParser) processStateTransition(ev trace.Event) {
	st := ev.StateTransition()
	if st.Resource.Kind != trace.ResourceGoroutine {
		return
	}

	goid := st.Resource.Goroutine()
	from, to := st.Goroutine()

	// 初始化 goroutine 信息
	ginfo, exists := tp.goroutines[goid]
	if !exists {
		ginfo = &GoroutineInfo{
			ID:       goid,
			ParentID: trace.NoGoroutine,
			Created:  ev.Time(),
			State:    to,
			Children: make([]trace.GoID, 0),
		}
		tp.goroutines[goid] = ginfo
	}
	ginfo.State = to

	// 检测 goroutine 创建
	if from == trace.GoNotExist && to == trace.GoRunnable {
		ginfo.Created = ev.Time()
		ginfo.ParentID = ev.Goroutine()
		
		// 继承父 goroutine 的 trace ID
		if parentTraceID, exists := tp.goroutineTraceIDs[ginfo.ParentID]; exists {
			ginfo.TraceID = parentTraceID
			tp.goroutineTraceIDs[goid] = parentTraceID
		}
	}

	// 检测 goroutine 结束
	if to == trace.GoSyscall || to == trace.GoWaiting {
		// 注意：在新版本的trace包中，可能需要根据具体的状态来判断goroutine是否结束
		// 这里使用一个简化的判断逻辑
		if to == trace.GoWaiting {
			ginfo.Finished = ev.Time()
		}
	}

	// 记录调用栈
	if stack := ev.Stack(); stack != trace.NoStack {
		frames := make([]trace.StackFrame, 0)
		for frame := range stack.Frames() {
			frames = append(frames, frame)
		}
		ginfo.Stack = frames
	}
}

// processRangeEvent 处理范围事件
func (tp *TraceParser) processRangeEvent(ev trace.Event) {
	// 处理用户定义的 region 和其他范围事件
	// 这可以帮助理解 goroutine 的执行上下文
}

// processTaskEvent 处理任务事件
func (tp *TraceParser) processTaskEvent(ev trace.Event) {
	// 处理 runtime/trace.NewTask 创建的任务
	// 这有助于理解 goroutine 之间的逻辑关系
}

// processLogEvent 处理日志事件
func (tp *TraceParser) processLogEvent(ev trace.Event) {
	// 处理 runtime/trace.Log 记录的日志
	// 提取用户注入的 trace ID
	logEvent := ev.Log()
	if logEvent.Message != "" {
		goid := ev.Goroutine()
		// 记录该 goroutine 的 trace ID
		tp.loggedTraceIDs[goid] = logEvent.Message
		tp.goroutineTraceIDs[goid] = logEvent.Message
		
		// 更新 goroutine 信息中的 trace ID
		if ginfo, exists := tp.goroutines[goid]; exists {
			ginfo.TraceID = logEvent.Message
		}
	}
}

// inferParentGoroutine 推断父 goroutine
func (tp *TraceParser) inferParentGoroutine(childID trace.GoID, ev trace.Event) {
	// 这是一个简化的实现
	// 实际中需要维护一个执行上下文栈来准确追踪父子关系

	// 通过时间戳和调用栈信息推断可能的父 goroutine
	eventTime := ev.Time()

	// 查找在创建时间附近活跃的 goroutine
	for goid, info := range tp.goroutines {
		if goid == childID {
			continue
		}

		// 如果该 goroutine 在子 goroutine 创建时是活跃的
		if info.Created < eventTime &&
			(info.Finished == 0 || info.Finished > eventTime) {
			// 简单的启发式：选择最近创建的活跃 goroutine 作为父 goroutine
			if tp.goroutines[childID].ParentID == trace.NoGoroutine ||
				info.Created > tp.goroutines[tp.goroutines[childID].ParentID].Created {
				tp.goroutines[childID].ParentID = goid
			}
		}
	}
}

// buildRelationships 构建父子关系映射
func (tp *TraceParser) buildRelationships() {
	for _, info := range tp.goroutines {
		if info.ParentID != trace.NoGoroutine {
			tp.relationships[info.ParentID] = append(tp.relationships[info.ParentID], info.ID)

			// 更新父 goroutine 的子列表
			if parent, exists := tp.goroutines[info.ParentID]; exists {
				parent.Children = append(parent.Children, info.ID)
			}
		}
	}
}

// GetGoroutineInfo 获取指定 goroutine 的信息
func (tp *TraceParser) GetGoroutineInfo(goid trace.GoID) (*GoroutineInfo, bool) {
	info, exists := tp.goroutines[goid]
	return info, exists
}

// GetChildren 获取指定 goroutine 的所有子 goroutine
func (tp *TraceParser) GetChildren(goid trace.GoID) []trace.GoID {
	return tp.relationships[goid]
}

// GetAllGoroutines 获取所有 goroutine 信息
func (tp *TraceParser) GetAllGoroutines() map[trace.GoID]*GoroutineInfo {
	return tp.goroutines
}

// PrintGoroutineTree 打印 goroutine 树形结构
func (tp *TraceParser) PrintGoroutineTree() {
	fmt.Println("Goroutine Relationship Tree:")
	fmt.Println("============================")

	// 找到根 goroutine（没有父 goroutine 的）
	roots := make([]trace.GoID, 0)
	for goid, info := range tp.goroutines {
		if info.ParentID == trace.NoGoroutine {
			roots = append(roots, goid)
		}
	}

	// 打印每个根 goroutine 的树
	for _, root := range roots {
		tp.printGoroutineSubtree(root, 0)
	}
}

// printGoroutineSubtree 递归打印 goroutine 子树
func (tp *TraceParser) printGoroutineSubtree(goid trace.GoID, depth int) {
	info, exists := tp.goroutines[goid]
	if !exists {
		return
	}

	indent := ""
	for i := 0; i < depth; i++ {
		indent += "  "
	}

	duration := "running"
	if info.Finished != 0 {
		durationNs := int64(info.Finished - info.Created)
		duration = time.Duration(durationNs).String()
	}

	fmt.Printf("%s├─ Goroutine %d (State: %v, Duration: %s)\n",
		indent, info.ID, info.State, duration)

	// 递归打印子 goroutine
	for _, child := range info.Children {
		tp.printGoroutineSubtree(child, depth+1)
	}
}

// GetGoroutineTraceID 获取指定 goroutine 的 trace ID
func (tp *TraceParser) GetGoroutineTraceID(goid trace.GoID) (string, bool) {
	traceID, exists := tp.goroutineTraceIDs[goid]
	return traceID, exists
}

// GetGoroutinesByTraceID 获取指定 trace ID 关联的所有 goroutine
func (tp *TraceParser) GetGoroutinesByTraceID(traceID string) []trace.GoID {
	var goroutines []trace.GoID
	for goid, id := range tp.goroutineTraceIDs {
		if id == traceID {
			goroutines = append(goroutines, goid)
		}
	}
	return goroutines
}

// GetAllTraceIDs 获取所有的 trace ID
func (tp *TraceParser) GetAllTraceIDs() []string {
	traceIDSet := make(map[string]bool)
	for _, traceID := range tp.goroutineTraceIDs {
		traceIDSet[traceID] = true
	}
	
	var traceIDs []string
	for traceID := range traceIDSet {
		traceIDs = append(traceIDs, traceID)
	}
	return traceIDs
}

// GetStatistics 获取统计信息
func (tp *TraceParser) GetStatistics() map[string]any {
	stats := make(map[string]any)

	totalGoroutines := len(tp.goroutines)
	stats["total_goroutines"] = totalGoroutines
	stats["total_events"] = len(tp.events)

	// 按状态统计
	stateStats := make(map[trace.GoState]int)
	var totalDuration time.Duration
	completedCount := 0

	for _, info := range tp.goroutines {
		stateStats[info.State]++

		if info.Finished != 0 {
			durationNs := int64(info.Finished - info.Created)
			totalDuration += time.Duration(durationNs)
			completedCount++
		}
	}

	stats["state_distribution"] = stateStats

	if completedCount > 0 {
		stats["average_goroutine_duration"] = totalDuration / time.Duration(completedCount)
	}

	// 计算树的深度
	maxDepth := tp.calculateMaxDepth()
	stats["max_goroutine_tree_depth"] = maxDepth
	
	// trace ID 统计
	stats["total_trace_ids"] = len(tp.GetAllTraceIDs())
	traceIDStats := make(map[string]int)
	for _, traceID := range tp.goroutineTraceIDs {
		traceIDStats[traceID]++
	}
	stats["trace_id_distribution"] = traceIDStats

	return stats
}

// calculateMaxDepth 计算 goroutine 树的最大深度
func (tp *TraceParser) calculateMaxDepth() int {
	maxDepth := 0

	// 找到根 goroutine
	for goid, info := range tp.goroutines {
		if info.ParentID == trace.NoGoroutine {
			depth := tp.calculateDepth(goid, 1)
			if depth > maxDepth {
				maxDepth = depth
			}
		}
	}

	return maxDepth
}

// calculateDepth 递归计算指定 goroutine 的深度
func (tp *TraceParser) calculateDepth(goid trace.GoID, currentDepth int) int {
	maxDepth := currentDepth

	for _, child := range tp.relationships[goid] {
		depth := tp.calculateDepth(child, currentDepth+1)
		if depth > maxDepth {
			maxDepth = depth
		}
	}

	return maxDepth
}
