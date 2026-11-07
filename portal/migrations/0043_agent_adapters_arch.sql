-- Adapter manifests gain an optional arch dimension. Rows with arch=''
-- match any architecture; a row with a concrete arch (e.g. 'amd64',
-- 'arm64') wins over the generic row for that client/os pair.
ALTER TABLE agent_adapters
    ADD COLUMN IF NOT EXISTS arch varchar(16) NOT NULL DEFAULT '';

DROP INDEX IF EXISTS agent_adapters_client_os_key;
ALTER TABLE agent_adapters
    DROP CONSTRAINT IF EXISTS agent_adapters_client_os_key;
ALTER TABLE agent_adapters
    ADD CONSTRAINT agent_adapters_client_os_arch_key UNIQUE (client, os, arch);
