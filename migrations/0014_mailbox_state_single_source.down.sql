-- The reconciliation corrected cached numbers and cannot be undone (and has no
-- reason to be); only the function is removed. Folders it added stay: they are
-- ordinary folders by now and may hold mail.
DROP FUNCTION IF EXISTS provision_default_folders(UUID);
