# What makes a good free model

A score of 2–10: how useful the model is to the owner for free:

- **9–10**: the level of the best paid models of its class, free and with limits enough for daily work.
- **7–8**: strong, clearly above average; worth connecting. A card is made from 7.
- **5–6**: fit for simple tasks (drafts, classification, labelling).
- **2–4**: weak, outdated, small or with limits that make it unusable. Do not show.

Take into account: public benchmarks and reviews, size and freshness, context length, support of tools (tool calls, which deck's agents need), the real limits of the free tier, whether it is a limited-time offer (stealth/preview models vanish: note it in limits).

Moderation/safety models, embedders and routers (`openrouter/free`) are not "strong models": a score of 4 at most.

# deck's roles

For each model give the deck roles it can take (`roles`), if any:

- `agent`: an agent with tools: needs tools and a long context;
- `text`: an answer as text or JSON by a schema: drafts, analysis, translation;
- `image`: pictures;
- `tts`: speech;
- `stt`: speech recognition;
- `video`: video clips;
- `music`: music and sounds.

If a role has no free cloud model but has a free local one, record that too with `source: local`.
