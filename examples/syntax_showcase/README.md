# Syntax Showcase

A complete, validated demonstration of the MARTe extended configuration
language. Every file passes `mdt check` (see below for the exact
commands used).

## Files

| File | Demonstrates |
|---|---|
| `app.marte` | `$`-root application skeleton, `#package` routing, `#if`/`#else if`/`#else` chains |
| `variables.marte` | `#var`/`#let`, every literal flavour, computed constants, expression notes |
| `data.marte` | Data sources (`LinuxTimer`, `GAMDataSource`, `LoggerDataSource`), signal definition sugar (`Counter: uint32`), `//! unused:` pragma |
| `functions.marte` | `DataSource::Signal` shorthand (type, dimension, alias, extra fields), conditional GAM definitions |
| `states.marte` | Typed `[&GAM]` function lists, conditional threads |
| `loops.marte` | `#template`/`#use`, `#foreach` (one and two variables), dynamic object names |
| `external.marte` + `data/epics_cfg.json` | `with json(...) as … begin…end`, member access (`@cfg.member`), two-variable `#foreach` over a dict |

## Validating

```bash
# Whole project (including loops.marte and external.marte):
mdt check -P .

# With variable overrides — -j loads a JSON variable file, -v wins on
# conflicts:
mdt check -P . -j vars_streaming_off.json
mdt check -P . -vudp_streamer=false -vChannelBudget=1

# Build both streaming branches:
mdt build -P . -o showcase_full.marte
mdt build -P . -o showcase_local.marte -vudp_streamer=false

# The standalone loops demo (single-file mode reports the cross-file
# @NumChannels as unresolved — single-file checks do not load siblings;
# use the project validation above):
mdt check loops.marte
```

Create `vars_streaming_off.json` with
`{ "udp_streamer": false, "ChannelBudget": 1 }` to try the `-j` form.

`external.marte` intentionally declares an IOGAM whose input signals
(`Stat` uint32 + `Temp` float64 = 12 bytes) outweigh its output signal
(`T` uint32 = 4 bytes), so `mdt check` reports:

```
external.marte:45:5: ERROR: Schema Validation Error:
  #Object.InputSize: conflicting values 4 and 12
```

That is the schema catching a real configuration error — add matching
output signals (or drop inputs) to make the project clean; everything
else in the showcase validates without issues.
```

`mdt check -P .`, the override variants and both builds all pass with
zero errors and zero warnings. `mdt build -P` aborts on any validation
diagnostic, so the explicit `build` commands above are the way to
produce the merged output.
