-- +goose Up
-- dismissed marks an expired confirmation the owner has cleared from what waits for them.
ALTER TABLE ask ADD COLUMN dismissed INTEGER NOT NULL DEFAULT 0;

-- verdict of the last self-check of a provider: model (the model does not manage it) or adapter (it broke);
-- speed is the tokens per second the model answered at in it.
ALTER TABLE health ADD COLUMN verdict TEXT NOT NULL DEFAULT '';
ALTER TABLE health ADD COLUMN speed REAL NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE health DROP COLUMN speed;
ALTER TABLE health DROP COLUMN verdict;
ALTER TABLE ask DROP COLUMN dismissed;
