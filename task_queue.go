package main

import "sync"
import "sync/atomic"

// ===== 单线程任务队列 =====
// 小双的所有"干活"操作（AI 聊天回复/HTTP 命令执行/记忆总结归档）统一排队，
// 由一个 worker 串行执行——发消息或派命令后必须等当前任务完成才能处理下一个，
// 避免并发调用 AI/执行命令导致的混乱。

type taskFunc func()

var (
	taskQueue    = make(chan taskFunc, 64)
	taskWorkerOn sync.Once
	taskBusy     atomic.Bool // 当前是否有任务在执行（忙时新消息直接提示）
)

// startTaskWorker 启动串行 worker（幂等，多次调用只起一个）
func startTaskWorker() {
	taskWorkerOn.Do(func() {
		go func() {
			for f := range taskQueue {
				taskBusy.Store(true)
				f()
				taskBusy.Store(false)
			}
		}()
	})
}

// isTaskBusy 小双是否正在忙（有任务在执行）
func isTaskBusy() bool {
	return taskBusy.Load()
}

// enqueue 排队执行（队列满则阻塞等待空位）
// 注意：任务内部不要再 enqueue 后同步等待结果，会占用 worker 造成死锁；
// 同步等待请用 done channel（见 handleExec 模式）。
func enqueue(f taskFunc) {
	taskQueue <- f
}
