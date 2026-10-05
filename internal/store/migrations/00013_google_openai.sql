-- +goose Up
-- deck asks models in the OpenAI-compatible protocol: a connection of the Google API moves to that address of Google.
UPDATE connection SET api = 'openai', url = 'https://generativelanguage.googleapis.com/v1beta/openai/'
WHERE api = 'google' AND url LIKE 'https://generativelanguage.googleapis.com/%';
