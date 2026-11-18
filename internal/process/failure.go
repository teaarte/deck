package process

// FailedRun is an open failure of a run as the owner's feed lists it: Title is its command's, RecordTitle the title of
// its record, Retry whether the owner may run it again, Repeats how many failures alike it stands for.
type FailedRun struct {
	ID                                   int64
	Project, Command, Title, RecordTitle string
	Error, ErrorType, FinishedAt         string
	Retry                                bool
	Repeats                              int64
}
