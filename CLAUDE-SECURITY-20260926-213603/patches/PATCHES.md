# Suggested patches

Targeted patches for findings in `CLAUDE-SECURITY-20260926-213603`, each written against revision `da3f5ff15143` and verified by a panel of agents before it was written. Nothing here is applied, committed, or opened as a pull request until you choose to do so.

## Patches written

- **F1** -- Image build/sign/release pipeline does not restrict the ref to main's ancestry, letting any pushed tag or dispatched branch become the repo-signed :1 image: `F1.patch` _(no project test of the patched code was run)_

## Set aside by the reviewers

- while reviewing **F1** -- Anyone with tag-push (write) access can tag an already-reviewed on-main commit with an arbitrary version and publish that major; inherent to tag-driven releasing, requires write access and builds only reviewed on-main code, pre-existing and unrelated to this finding

Each of these was seen while reviewing the finding named beside it and was set aside as outside that finding, so nothing written for that finding addresses it. Unless another finding here covers it, review it yourself, or ask Claude Security to scan or patch it.

## Applying a patch

From the repository root:

```
git apply CLAUDE-SECURITY-20260926-213603/patches/F<n>.patch
```

Each `F<n>.md` beside the patch explains the change and what was verified. The job that wrote these applied, committed, pushed, and opened nothing; if you want one applied, or turned into a pull request, ask Claude Security and it handles that as a separate request.
