-- Only removes the folder where it is still empty. A Reports folder holding
-- delivered reports is data, and dropping it would cascade those messages away.
DELETE FROM folders f
 WHERE LOWER(f.name) = 'reports'
   AND f.special_use IS NULL
   AND NOT EXISTS (
     SELECT 1 FROM email_messages e WHERE e.folder_id = f.id
   );
