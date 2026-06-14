-- Create FTS5 virtual table for full-text search on nodes
CREATE VIRTUAL TABLE IF NOT EXISTS nodes_fts USING fts5(
    content=nodes,
    name,
    note,
    tokenize="porter unicode61 categories 'L* N* Co Ps Pe'"
);

-- Create triggers to keep nodes_fts in sync with nodes
CREATE TRIGGER IF NOT EXISTS nodes_insert AFTER INSERT ON nodes BEGIN
    INSERT INTO nodes_fts(rowid, name, note) VALUES (new.id, new.name, new.note);
END;
CREATE TRIGGER IF NOT EXISTS nodes_delete AFTER DELETE ON nodes BEGIN
    INSERT INTO nodes_fts(nodes_fts, rowid, name, note) VALUES ('delete', old.id, old.name, old.note);
END;
CREATE TRIGGER IF NOT EXISTS nodes_update AFTER UPDATE ON nodes BEGIN
    INSERT INTO nodes_fts(nodes_fts, rowid, name, note) VALUES ('delete', old.id, old.name, old.note);
    INSERT INTO nodes_fts(rowid, name, note) VALUES (new.id, new.name, new.note);
END;
