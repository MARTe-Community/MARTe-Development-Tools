# Complex example: copy / fan-out sugar and pipeline macros

This project exercises the experimental build-time sugar (see
[docs/COPY_SUGAR.md](../../docs/COPY_SUGAR.md)):

| File | Shows |
|---|---|
| `src/components.marte` | data sources and the signals the wiring lands in; `AllowNoProducers = 1` on the working DataSources |
| `src/pipelines.marte` | a reusable `gam Preprocess` macro with two stages, input/output ports and `from … -> …` wiring |
| `src/app.marte` | pipeline instantiation, a named copy group fan-out with aliases and `at` indexes, `from` wiring in a GAM input list, and `fuse Bias with Clamp` |

Build it:

```bash
mdt check -P .
mdt build -P . -O -o build/complex.marte
```

The build reports what the sugar expanded to, for example:

```
Copies: 7 copy pair(s), 1 direct wire(s), 1 GAM pair(s) fused, 1 pipeline(s) expanded
Optimisations: reordered 2 signal(s) in 1 DataSource(s), grouped 1 GAM signal list(s)
```

`mdt check` still warns about telemetry signals that are produced but never
consumed in the same thread (`Out`, `Meter`, `StatCopy`, `Frame`) — that is
expected for sink signals.
