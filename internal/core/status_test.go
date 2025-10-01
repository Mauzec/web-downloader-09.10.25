package core

import "testing"

func newFile(st FileStatus) *File {
	return &File{Status: st}
}

func TestObtainStatus(t *testing.T) {
	testCases := []struct {
		name  string
		files []*File
		want  TaskStatus
	}{
		{
			name:  "all completed",
			files: []*File{newFile(FileStatusCompleted), newFile(FileStatusCompleted)},
			want:  TaskStatusCompleted,
		},
		{
			name:  "all error",
			files: []*File{newFile(FileStatusError), newFile(FileStatusError)},
			want:  TaskStatusFailed,
		},
		{
			name:  "all waiting",
			files: []*File{newFile(FileStatusWaiting), newFile(FileStatusWaiting)},
			want:  TaskStatusPending,
		},
		{
			name:  "complete and error",
			files: []*File{newFile(FileStatusError), newFile(FileStatusCompleted)},
			want:  TaskStatusPartiallyCompleted,
		},
		{
			name:  "waiting and finished",
			files: []*File{newFile(FileStatusWaiting), newFile(FileStatusCompleted)},
			want:  TaskStatusInProgress,
		},
		{
			name:  "no files",
			files: nil,
			want:  TaskStatusCompleted,
		},
		{
			name:  "downloading",
			files: []*File{newFile(FileStatusWaiting), newFile(FileStatusDownloading)},
			want:  TaskStatusInProgress,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := obtainStatus(tc.files)
			if got != tc.want {
				t.Fatalf(
					"obtainStatus: got %v, want %v",
					got, tc.want,
				)
			}
		})
	}
}
