package z

func f(s S) error {
	_ = s.Tx(nil, nil)
	s.Tx(nil, nil)
	_, _ = store.TxGet[int](s, nil, nil)
	defer s.St.Tx(nil, nil)
	n, _ := store.TxGet(s, nil, nil)
	_, _ = TxGet(s, nil, nil)
	n, err := TxGet(s, nil, nil)
	if err := s.Tx(nil, nil); err != nil {
		return err
	}
	return s.Tx(nil, nil)
}
