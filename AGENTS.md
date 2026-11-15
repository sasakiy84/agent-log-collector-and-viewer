# Development workflow

- Start each change on a dedicated branch based on the latest `main`.
- Commit and push the branch, then open a pull request targeting `main`.
- PR のタイトルと本文は日本語で書く。
- Run the local checks appropriate to the change before merging.
- Merge through the pull request. The user authorizes merging without asking for additional confirmation.
- Do not push implementation or documentation commits directly to `main`.
- Create release tags from merged commits.
