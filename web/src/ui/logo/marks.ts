// Marks of models, their makers and providers: SVG of @lobehub/icons-static-svg (MIT), only these, in deck's build.
// A word of a name takes the mark of the first rule it starts with, followed by no letter.

import ai21 from '@lobehub/icons-static-svg/icons/ai21.svg?raw';
import alibaba_color from '@lobehub/icons-static-svg/icons/alibaba-color.svg?raw';
import anthropic from '@lobehub/icons-static-svg/icons/anthropic.svg?raw';
import aws_color from '@lobehub/icons-static-svg/icons/aws-color.svg?raw';
import azure_color from '@lobehub/icons-static-svg/icons/azure-color.svg?raw';
import baichuan_color from '@lobehub/icons-static-svg/icons/baichuan-color.svg?raw';
import bedrock_color from '@lobehub/icons-static-svg/icons/bedrock-color.svg?raw';
import bfl from '@lobehub/icons-static-svg/icons/bfl.svg?raw';
import bytedance_color from '@lobehub/icons-static-svg/icons/bytedance-color.svg?raw';
import cerebras_color from '@lobehub/icons-static-svg/icons/cerebras-color.svg?raw';
import chatglm_color from '@lobehub/icons-static-svg/icons/chatglm-color.svg?raw';
import claude_color from '@lobehub/icons-static-svg/icons/claude-color.svg?raw';
import codex_color from '@lobehub/icons-static-svg/icons/codex-color.svg?raw';
import cohere_color from '@lobehub/icons-static-svg/icons/cohere-color.svg?raw';
import dalle_color from '@lobehub/icons-static-svg/icons/dalle-color.svg?raw';
import deepinfra_color from '@lobehub/icons-static-svg/icons/deepinfra-color.svg?raw';
import deepseek_color from '@lobehub/icons-static-svg/icons/deepseek-color.svg?raw';
import doubao_color from '@lobehub/icons-static-svg/icons/doubao-color.svg?raw';
import elevenlabs from '@lobehub/icons-static-svg/icons/elevenlabs.svg?raw';
import fal_color from '@lobehub/icons-static-svg/icons/fal-color.svg?raw';
import fireworks_color from '@lobehub/icons-static-svg/icons/fireworks-color.svg?raw';
import fishaudio from '@lobehub/icons-static-svg/icons/fishaudio.svg?raw';
import flux from '@lobehub/icons-static-svg/icons/flux.svg?raw';
import gemini_color from '@lobehub/icons-static-svg/icons/gemini-color.svg?raw';
import gemma_color from '@lobehub/icons-static-svg/icons/gemma-color.svg?raw';
import google_color from '@lobehub/icons-static-svg/icons/google-color.svg?raw';
import goose from '@lobehub/icons-static-svg/icons/goose.svg?raw';
import grok from '@lobehub/icons-static-svg/icons/grok.svg?raw';
import groq from '@lobehub/icons-static-svg/icons/groq.svg?raw';
import huggingface_color from '@lobehub/icons-static-svg/icons/huggingface-color.svg?raw';
import hunyuan_color from '@lobehub/icons-static-svg/icons/hunyuan-color.svg?raw';
import ibm from '@lobehub/icons-static-svg/icons/ibm.svg?raw';
import ideogram from '@lobehub/icons-static-svg/icons/ideogram.svg?raw';
import internlm_color from '@lobehub/icons-static-svg/icons/internlm-color.svg?raw';
import kimi_color from '@lobehub/icons-static-svg/icons/kimi-color.svg?raw';
import kling_color from '@lobehub/icons-static-svg/icons/kling-color.svg?raw';
import liquid from '@lobehub/icons-static-svg/icons/liquid.svg?raw';
import lmstudio from '@lobehub/icons-static-svg/icons/lmstudio.svg?raw';
import luma_color from '@lobehub/icons-static-svg/icons/luma-color.svg?raw';
import meta_color from '@lobehub/icons-static-svg/icons/meta-color.svg?raw';
import microsoft_color from '@lobehub/icons-static-svg/icons/microsoft-color.svg?raw';
import midjourney from '@lobehub/icons-static-svg/icons/midjourney.svg?raw';
import minimax_color from '@lobehub/icons-static-svg/icons/minimax-color.svg?raw';
import mistral_color from '@lobehub/icons-static-svg/icons/mistral-color.svg?raw';
import moonshot from '@lobehub/icons-static-svg/icons/moonshot.svg?raw';
import novita_color from '@lobehub/icons-static-svg/icons/novita-color.svg?raw';
import nvidia_color from '@lobehub/icons-static-svg/icons/nvidia-color.svg?raw';
import ollama from '@lobehub/icons-static-svg/icons/ollama.svg?raw';
import openai from '@lobehub/icons-static-svg/icons/openai.svg?raw';
import openrouter from '@lobehub/icons-static-svg/icons/openrouter.svg?raw';
import perplexity_color from '@lobehub/icons-static-svg/icons/perplexity-color.svg?raw';
import qwen_color from '@lobehub/icons-static-svg/icons/qwen-color.svg?raw';
import replicate from '@lobehub/icons-static-svg/icons/replicate.svg?raw';
import runway from '@lobehub/icons-static-svg/icons/runway.svg?raw';
import sambanova_color from '@lobehub/icons-static-svg/icons/sambanova-color.svg?raw';
import siliconcloud_color from '@lobehub/icons-static-svg/icons/siliconcloud-color.svg?raw';
import stability_color from '@lobehub/icons-static-svg/icons/stability-color.svg?raw';
import suno from '@lobehub/icons-static-svg/icons/suno.svg?raw';
import together_color from '@lobehub/icons-static-svg/icons/together-color.svg?raw';
import vertexai_color from '@lobehub/icons-static-svg/icons/vertexai-color.svg?raw';
import xai from '@lobehub/icons-static-svg/icons/xai.svg?raw';
import yi_color from '@lobehub/icons-static-svg/icons/yi-color.svg?raw';
import zhipu_color from '@lobehub/icons-static-svg/icons/zhipu-color.svg?raw';

// rules are the starts of words and the mark each names, the more exact first.
export const rules: [string, string][] = [
  ['claude', claude_color], ['anthropic', anthropic], ['sonnet', claude_color], ['opus', claude_color],
  ['haiku', claude_color], ['codex', codex_color], ['openai', openai], ['chatgpt', openai], ['gpt', openai],
  ['o1', openai], ['o3', openai], ['o4', openai], ['whisper', openai], ['dall', dalle_color], ['sora', openai],
  ['gemini', gemini_color], ['gemma', gemma_color], ['google', google_color], ['vertex', vertexai_color],
  ['imagen', google_color], ['veo', google_color], ['qwen', qwen_color], ['qwq', qwen_color],
  ['alibaba', alibaba_color], ['dashscope', alibaba_color], ['deepseek', deepseek_color], ['llama', meta_color],
  ['meta', meta_color], ['mistral', mistral_color], ['mixtral', mistral_color], ['codestral', mistral_color],
  ['devstral', mistral_color], ['magistral', mistral_color], ['pixtral', mistral_color],
  ['ministral', mistral_color], ['voxtral', mistral_color], ['ollama', ollama], ['openrouter', openrouter],
  ['nemotron', nvidia_color], ['nvidia', nvidia_color], ['nim', nvidia_color], ['grok', grok], ['xai', xai],
  ['kimi', kimi_color], ['moonshot', moonshot], ['glm', zhipu_color], ['zhipu', zhipu_color],
  ['chatglm', chatglm_color], ['z-ai', zhipu_color], ['minimax', minimax_color], ['cohere', cohere_color],
  ['command', cohere_color], ['groq', groq], ['goose', goose], ['lmstudio', lmstudio], ['lm-studio', lmstudio],
  ['huggingface', huggingface_color], ['hf', huggingface_color], ['flux', flux], ['bfl', bfl],
  ['stability', stability_color], ['sdxl', stability_color], ['stable', stability_color], ['elevenlabs', elevenlabs],
  ['suno', suno], ['kling', kling_color], ['hunyuan', hunyuan_color], ['doubao', doubao_color],
  ['seed', bytedance_color], ['bytedance', bytedance_color], ['phi', microsoft_color],
  ['microsoft', microsoft_color], ['azure', azure_color], ['perplexity', perplexity_color],
  ['sonar', perplexity_color], ['together', together_color], ['fireworks', fireworks_color],
  ['cerebras', cerebras_color], ['sambanova', sambanova_color], ['deepinfra', deepinfra_color],
  ['novita', novita_color], ['siliconflow', siliconcloud_color], ['siliconcloud', siliconcloud_color],
  ['replicate', replicate], ['fal', fal_color], ['runway', runway], ['luma', luma_color], ['ideogram', ideogram],
  ['midjourney', midjourney], ['yi', yi_color], ['baichuan', baichuan_color], ['internlm', internlm_color],
  ['granite', ibm], ['ibm', ibm], ['liquid', liquid], ['lfm', liquid], ['aws', aws_color],
  ['bedrock', bedrock_color], ['nova', aws_color], ['ai21', ai21], ['jamba', ai21], ['fish', fishaudio],
];
