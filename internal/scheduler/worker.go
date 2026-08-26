package scheduler

import "github.com/sebishogun/nornrune/internal/eval"

func (scheduler *Scheduler) worker() {
	defer scheduler.workerDone.Done()
	for job := range scheduler.jobs {
		scheduler.runShard(job.state, job.index)
	}
}

func (scheduler *Scheduler) runShard(state *batchState, index int) {
	state.errors[index] = scheduler.executeShard(state, index)
	scheduler.workTokens <- struct{}{}
	state.done.Done()
}

func (scheduler *Scheduler) executeShard(state *batchState, index int) error {
	if err := state.ctx.Err(); err != nil {
		return err
	}
	lease, err := scheduler.arena.Borrow(state.ctx)
	if err != nil {
		return err
	}
	if err = state.ctx.Err(); err == nil {
		rangeRows := state.ranges[index]
		rows := rangeRows.end - rangeRows.start
		err = lease.Grow(Capacity{Rows: rows})
		if err == nil {
			var executor *eval.Executor
			executor, err = lease.Executor()
			if err == nil {
				if rangeRows.start == 0 && rangeRows.end == state.batch.Rows {
					err = executor.Execute(&state.results[index], state.program, state.batch)
				} else {
					scratchLen := uint64(rows) + 1
					if scratchLen > uint64(cap(lease.context.evidenceOffsets)) {
						err = ErrInvalidCapacity
					} else {
						scratch := lease.context.evidenceOffsets[:int(scratchLen)]
						err = executor.ExecuteRange(
							&state.results[index], state.program, state.batch,
							rangeRows.start, rangeRows.end, scratch,
						)
					}
				}
			}
		}
	}
	returnErr := scheduler.arena.Return(lease)
	if err == nil {
		return returnErr
	}
	return err
}
