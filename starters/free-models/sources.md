# Источники бесплатных моделей

Скрипт `collect` сам читает списки:

- **OpenRouter** — `https://openrouter.ai/api/v1/models`, бесплатные: цена 0 или `:free` в id. Ключ: openrouter.ai → Keys. Лимиты free: ~20 запросов/мин, 50/день без пополнения, 1000/день после разового пополнения на $10.
- **NVIDIA NIM** — `https://integrate.api.nvidia.com/v1/models`. Ключ: build.nvidia.com → Get API Key (бесплатно для разработки, лимит по запросам в минуту).

Агент `review` раз в день проверяет и остальные (сам ищет в вебе изменения):

- **Google AI Studio** (Gemini, Gemini TTS, Imagen/Veo где есть free tier) — aistudio.google.com → Get API key.
- **Groq** — быстрые LLM и Whisper (распознавание речи), console.groq.com.
- **Cerebras** — быстрые LLM, cloud.cerebras.ai.
- **Hugging Face Inference / Spaces** — бесплатные кредиты, картинки (FLUX), TTS.
- **Cloudflare Workers AI** — бесплатная дневная квота: LLM, картинки, Whisper, TTS.
- **Mistral La Plateforme** (free tier), **GitHub Models**, **Pollinations** (картинки без ключа).
- **Локальные открытые модели** для звука и видео, которые можно запустить на Mac бесплатно: Whisper / whisper.cpp, Kokoro / Piper TTS, FLUX / SDXL через ComfyUI или Draw Things, LTX-Video / Wan.

Владелец может дописать сюда источники — агент читает этот документ каждый раз.
