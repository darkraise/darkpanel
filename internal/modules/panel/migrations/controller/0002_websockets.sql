-- Whether the exposure passes WebSocket upgrades (spec M11 section 5). An NPM
-- exposure made before this column existed has a proxy host without them and
-- shows 0 until panel.websockets.repair turns them on; a Cloudflare tunnel
-- passes them natively.
ALTER TABLE panel_exposures ADD COLUMN websockets INTEGER NOT NULL DEFAULT 0 CHECK (websockets IN (0, 1));

UPDATE panel_exposures SET websockets = 1 WHERE via = 'tunnel';
