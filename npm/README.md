# @adviverse/cli

The Adviverse command line, as an npm package. It downloads the matching binary from
[github.com/Adviverse/cli](https://github.com/Adviverse/cli/releases) on first run.

```bash
npx @adviverse/cli doctor --site https://example.com
export ADVIVERSE_API_KEY=...   # Settings → API keys
npx @adviverse/cli properties list
npx @adviverse/cli plan -f adviverse.yaml && npx @adviverse/cli apply -f adviverse.yaml
```
