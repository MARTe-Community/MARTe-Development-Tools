# external_ref

Demonstrates replacing an external templating step with the language's own
structured-data features.

- `ex.marte.template` — the original template: `{% for %}`/{% if %} blocks
  expanding `context["dan_config"]` (programs → streams → variables) into
  signal declarations.
- `ex.marte` — the equivalent standard configuration: a
  `with json(@dan_config_file) as dan` block plus `#foreach`/`#if` over
  `@dan.dan_config`, `@program.Streams` and `@stream.Variables`.
- `dan_config.json` — the data, in the same shape the template consumed.
  `name` carries the raw signal name; the configuration strips the
  `EC-GN-P01:` prefix itself with `.replace()`.
- `.marte_schema.cue` — project schema declaring the site-specific classes
  (`NI6368::NI6368ADC`, `DAN::DANSource`, …) so they validate. Register them
  under the suffix name: the validator resolves `A::B` as `B`.

Verify with `mdt check -P .` (clean) and `mdt build -P . -o out.marte`
(the foreach blocks expand all `FastAnalogDAN_float32` variables).
