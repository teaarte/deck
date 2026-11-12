# Sources of free models

The script `collect` reads these lists itself:

- **OpenRouter**: `https://openrouter.ai/api/v1/models`, free ones: price 0 or `:free` in the id. Key: openrouter.ai → Keys. Free limits: ~20 requests a minute, 50 a day without a top-up, 1000 a day after a one-time top-up of $10.
- **NVIDIA NIM**: `https://integrate.api.nvidia.com/v1/models`. Key: build.nvidia.com → Get API Key (free for development, limited by requests a minute).

The agent `review` checks the others once a day (it searches the web for changes itself):

- **Google AI Studio** (Gemini, Gemini TTS, Imagen/Veo where there is a free tier): aistudio.google.com → Get API key.
- **Groq**: fast LLMs and Whisper (speech recognition), console.groq.com.
- **Cerebras**: fast LLMs, cloud.cerebras.ai.
- **Hugging Face Inference / Spaces**: free credits, pictures (FLUX), TTS.
- **Cloudflare Workers AI**: a free daily quota: LLMs, pictures, Whisper, TTS.
- **Mistral La Plateforme** (free tier), **GitHub Models**, **Pollinations** (pictures without a key).
- **Local open models** for sound and video that run on a Mac for free: Whisper / whisper.cpp, Kokoro / Piper TTS, FLUX / SDXL through ComfyUI or Draw Things, LTX-Video / Wan.

The owner may add sources here: the agent reads this document every time.
