# Development workflow

- Start each change on a dedicated branch based on the latest `main`.
- Commit and push the branch, then open a pull request targeting `main`.
- Verify the change and wait for the pull request's required checks to pass before merging.
- Merge through the pull request. The user authorizes merging without asking for additional confirmation.
- Do not push implementation or documentation commits directly to `main`.
- Create release tags from merged commits.
