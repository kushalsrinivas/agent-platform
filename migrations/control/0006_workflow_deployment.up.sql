-- Active deployment pointer. Publishing a version sets it; publishing an older published version rolls back.
ALTER TABLE control.workflows
    ADD COLUMN deployed_version integer
        CHECK (deployed_version IS NULL OR (deployed_version >= 1 AND deployed_version <= latest_version));
