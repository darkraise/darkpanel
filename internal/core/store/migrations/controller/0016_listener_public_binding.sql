-- public_binding is the host binding an NPM exposure's proxy publishes its
-- HTTPS port on, "" for a listener that serves a tunnel. NPM publishes 443 on
-- every address, so a listener recorded before the column existed gets that.

ALTER TABLE core_listeners ADD COLUMN public_binding TEXT NOT NULL DEFAULT '';

UPDATE core_listeners SET public_binding = '0.0.0.0:443' WHERE client_header = 'X-Darkpanel-Client';
