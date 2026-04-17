# scripts/tests

Plain-bash regression tests for shell scripts in `scripts/`.

## Running

Each test is a self-contained executable. Run it directly:

```
./scripts/tests/orchardist-serial-driver.test.sh
```

Exit 0 = pass. Non-zero = fail with a message on stderr.

## Tests

| File | What it covers |
|------|---------------|
| `orchardist-serial-driver.test.sh` | Regression for issue #243: driver must exit within 5 s when its target tmux session is killed. |
