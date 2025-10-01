package core

// TaskStatus is a state of a Task.
type TaskStatus string

type FileStatus string

const (
	TaskStatusPending    TaskStatus = "TASK_PENDING"
	TaskStatusInProgress TaskStatus = "TASK_IN_PROGRESS"
	TaskStatusFailed     TaskStatus = "TASK_FAILED"
	// PartiallyCompleted is used when some files finished, but some others failed.
	TaskStatusPartiallyCompleted TaskStatus = "TASK_PARTIALLY_COMPLETED"
	TaskStatusCompleted          TaskStatus = "TASK_COMPLETED"

	FileStatusWaiting     FileStatus = "FILE_WAITING" // Not started
	FileStatusQueued      FileStatus = "FILE_QUEUED"
	FileStatusDownloading FileStatus = "FILE_DOWNLOADING"
	FileStatusCompleted   FileStatus = "FILE_COMPLETED"
	FileStatusError       FileStatus = "FILE_ERROR"
)

// obtainStatus obtains task status. Returns:
//   - No files: completed
//   - Some file is downloading: in progress
//   - All file is waiting: pending
//   - All done: completed
//   - All error: failed
//   - Some error, some done: partially completed
func obtainStatus(files []*File) TaskStatus {
	n := len(files)
	if n == 0 {
		return TaskStatusCompleted
	}
	var wait, download, complete, error int

	for _, f := range files {
		switch f.Status {
		case FileStatusCompleted:
			complete++
		case FileStatusError:
			error++
		case FileStatusQueued, FileStatusDownloading:
			download++
		case FileStatusWaiting:
			wait++
		}
	}
	if download > 0 {
		return TaskStatusInProgress
	}
	if n == wait {
		return TaskStatusPending
	}
	finish := complete + error
	if wait > 0 && finish > 0 {
		return TaskStatusInProgress
	}
	if n == finish {
		if n == complete {
			return TaskStatusCompleted
		}
		if n == error {
			return TaskStatusFailed
		}
		return TaskStatusPartiallyCompleted
	}
	return TaskStatusInProgress
}
