package harness

// VideosManifest is a module of short videos: a media block plays each video in the browser's own player.
const VideosManifest = `{"version": 13,
  "collections": [{"name": "videos", "description": "Videos", "key": "slug", "display": "slug",
    "schema": {"type": "object", "properties": {"slug": {"type": "string"}, "file": {"type": "string"}, "length": {"type": "number"}}, "required": ["slug"]}}],
  "screens": [{"name": "watch", "title": "Watch", "blocks": [
    {"type": "media", "title": "Videos", "span": "full", "source": {"collection": "videos", "sort": "slug"},
      "item": {"file": "file", "title": "slug"}},
    {"type": "table", "source": {"collection": "videos"}, "columns": [{"label": "Length", "bind": "length", "format": "duration"}]},
    {"type": "list", "source": {"collection": "videos"}, "item": {"title": "slug"}},
    {"type": "chart", "source": {"collection": "videos"}, "x": "slug", "series": [{"label": "Length", "y": "length"}]},
    {"type": "actions", "actions": [{"label": "Again", "screen": "watch"}]}]}],
  "menu": ["watch"]}`
