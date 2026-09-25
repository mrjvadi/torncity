-- 0031_client_devices, reversed. Every linked game client is signed out.

BEGIN;

DROP TABLE client_refresh_tokens;
DROP TABLE client_devices;

COMMIT;
