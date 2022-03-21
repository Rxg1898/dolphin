package taskjob

import (
	"github.com/toolkits/pkg/logger"

	"github.com/rxg456/dolphin/api/models"
	serverRpc "github.com/rxg456/dolphin/api/modules/server/rpc"
)

var Locals *LocalTaskT

func InitLocals(metaDir string) {
	Locals = &LocalTaskT{
		M:       make(map[int64]*Task),
		MetaDir: metaDir,
	}
}

// 定义管理task的taskmap
type LocalTaskT struct {
	M       map[int64]*Task
	MetaDir string // 任务的本地存储目录
}

// 上报任务的结果方法
func (lt *LocalTaskT) ReportTasks() []serverRpc.ReportTask {
	ret := make([]serverRpc.ReportTask, 0, len(lt.M))
	for id, t := range lt.M {
		rt := serverRpc.ReportTask{Id: id, Clock: t.Clock}

		rt.Status = t.GetStatus()
		if rt.Status == "running" || rt.Status == "killing" {
			continue
		}
		rt.Stdout = t.GetStdout()
		rt.Stderr = t.GetStderr()

		stdoutLen := len(rt.Stdout)
		stderrLen := len(rt.Stderr)

		// 输出太长的话，截断，要不然把数据库撑爆了
		if stdoutLen > 65535 {
			start := stdoutLen - 65535
			rt.Stdout = rt.Stdout[start:]
		}

		if stderrLen > 65535 {
			start := stderrLen - 65535
			rt.Stderr = rt.Stderr[start:]
		}

		ret = append(ret, rt)

	}

	return ret
}

// 根据任务id获取task
func (lt *LocalTaskT) GetTask(id int64) (*Task, bool) {
	t, found := lt.M[id]
	return t, found
}

// 把任务设置到本地map中
func (lt *LocalTaskT) SetTask(t *Task) {
	lt.M[t.Id] = t
}

// 分配任务，首先从本地map中获取任务
// 如果找到了就更新任务的动作，比如kill任务
// 然后启动新的任务，将task塞入map中管理
func (lt *LocalTaskT) AssignTask(at *models.TaskMeta) {
	local, found := lt.GetTask(at.Id)
	if found {
		if local.Clock == at.Clock && local.Action == at.Action {
			return
		}
		local.Clock = at.Clock
		local.Action = at.Action
	} else {
		if at.Action == "kill" {
			// no process in local, no need kill
			return
		}
		if at.Action == "" {
			at.Action = "start"
		}
		local = &Task{
			Id:      at.Id,
			JobId:   at.Id,
			Clock:   at.Clock,
			Action:  at.Action,
			Account: at.Account,
			Args:    at.Args,
			Script:  at.Script,
			MetaDir: lt.MetaDir,
		}
		lt.SetTask(local)

		if local.doneBefore() {
			local.loadResult()
			return
		}
	}

	if local.Action == "kill" {
		local.SetStatus("killing")
		local.kill()
	} else if local.Action == "start" {
		local.SetStatus("running")
		local.start()
	} else {
		logger.Warningf("unknown actions: %s of task %d", at.Action, at.Id)
	}
}

// 清理任务的方法
func (lt *LocalTaskT) Clean(assigned map[int64]struct{}) {
	del := make(map[int64]struct{})

	for id := range lt.M {
		if _, found := assigned[id]; !found {
			del[id] = struct{}{}
		}
	}

	for id := range del {
		// 远端已经不关注这个任务了，但是本地来看，任务还是running的
		// 可能是远端认为超时了，此时本地不能删除，仍然要继续上报
		if lt.M[id].GetStatus() == "running" {
			continue
		}

		lt.M[id].ResetBuff()
		cmd := lt.M[id].Cmd
		delete(lt.M, id)
		if cmd != nil && cmd.Process != nil {
			cmd.Process.Release()
		}
	}
}
