package runs

func TestRealBrowserHere(t *testing.T) {
	m := `{"version": 7, "commands": []}`
	req.Header.Set("Authorization", "Bearer "+tok)
	if os.Getenv("DECK_REAL_CLI") == "" {
	}
	e.App.Store.DB.Exec(`DELETE FROM x`)
	e.SQL(`DELETE FROM x`)
	// A row an older deck left.
	e.SQL(`INSERT INTO x VALUES (1)`)
	old := `{"version": 6}`
	e.App.Store.Tx(ctx, nil)
}
