-- retires holds the exposure id a forward composite's step retires once it
-- has committed (1.5); it is empty for every other step.

ALTER TABLE core_composite_steps ADD COLUMN retires TEXT NOT NULL DEFAULT '';
