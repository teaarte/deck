package harness

type DeskCard struct {
	ID         string
	X, Y, W, H int64
}

type DeskView struct {
	Columns int64
	Own     bool
	Cards   []DeskCard
}
