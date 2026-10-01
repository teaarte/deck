---
name: topic-summary
description: Keeps the summary of a chat topic with the owner, so another provider or a new session can go on from it. Use when deck says a topic has grown since its summary, or before a long topic changes course.
---

# The summary of a topic

deck keeps every message of a topic. A session that did not see the earlier part — another provider, or a
session that was lost — gets a retelling: the topic's summary, then the messages after the summary's last
revision, word for word, at most 30 of them. Whatever is older and not in the summary is gone for that
session. So the summary has to carry everything that still matters.

Write it with `document_save`: project `master`, kind `doc`, the name deck gives (`topics/<topic>/summary.md`),
the whole text each time (it is a new revision, not an append), reason "topic summary".

What goes in, as short lines under these headings:

- **Goal** — what the owner wants from this topic, in one or two lines.
- **Decided** — what the owner chose or said yes to, with the numbers of cards, processes, projects and
  documents it touched.
- **Done** — what was made and where it is.
- **Open** — what waits: a question to the owner, a confirmation, a process still going, the next step.
- **Owner's words** — exact phrases that matter later (names, limits, "never …"), quoted.

Leave out greetings, your reasoning, tool output and anything a session can read again from deck (the text of
a card, a manifest). Keep it under 60 lines; when it grows past that, merge old "Done" lines into one. Write it
in the language of the topic.

Update it when deck says the topic has grown since the summary (it counts the messages for you), and when the
topic turns to a new goal. A short topic needs no summary.
