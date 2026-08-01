# Ordered Tasks

Each task is stored in an individual Markdown file. Its directory is the source
of truth for its status:

- `todo/` contains work that has not been completed.
- `done/` contains work whose pull request has been reviewed and merged.
- `cancelled/` contains work that will not be completed, with the reason recorded
  in the task file.

Tasks are executed in task-ID order unless dependencies permit an explicitly
approved exception. A task is complete only after its pull request is reviewed
and merged. At that point, add the pull request link as completion evidence and
move the task file from `todo/` to `done/`.

Task filenames begin with their stable task ID so ordinary directory listings
retain the intended order. Moving a file between status directories must not
change its filename, identifier, description, dependencies, or existing
evidence.
