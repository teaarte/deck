Finds new job postings every morning, rates them against my criteria and prepares CV and cover letter for the ones I pick.

# Vacancies

- Every day at 07:00 the `collect` script reads each record of `sources` from its cursor and writes only new
  records into `postings`. A source that cannot be read gets `status: error` and `last_error`; the others go on.
- Then a chain runs: `prepare` (script) takes up to 150 of the oldest postings without a rating as short
  excerpts → `rate` (agent) judges them against `criteria.md` → `apply` (script) writes the ratings into the
  records and puts postings that fit or may fit on the board in lane **Found**. Rejected ones stay only as
  records and one line in the result of `rate` — that result is the daily summary. What does not fit into one
  batch waits for the next morning, or the owner runs "Rate waiting postings" again.
- The owner moves a card to **Interesting** when they want to apply. That starts `package`: a CV tailored to the
  posting and a short cover letter in English, only from `facts.md`, saved as artifacts on the card; the card then
  moves to **Package ready**.
- **Sent**, **Reply** and **Archive** are the owner's own lanes. Sending is manual: the owner downloads and copies
  the package. Nothing leaves deck on its own.

Documents: `criteria.md` (what fits), `facts.md` (facts about the owner — the only source for CV and letter).
