实现一个golang runtime trace文件的解析能力，trace文件是通过"runtime/trace"采集的。包含以下功能：
1. 解析 goroutine 相关的 event，分析 goroutine 的父子关系。
2. 解析 trace.Log() 注入的id（string类型），将后续产生的 goroutine 都关联到这个id。