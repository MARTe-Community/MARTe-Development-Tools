# big_project, refactored with copy groups and a pipeline

This is the reference `examples/big_project` rewritten with the experimental
copy sugar (see [docs/COPY_SUGAR.md](../../docs/COPY_SUGAR.md)). The original
project is kept unchanged next to it.

## What changed

Every IOGAM except `EPICSOutputGAM` was replaced by a named copy group with
the same GAM name, position and pair order — 22 groups, 302 copy pairs.
Per-signal options (`Type`, `Samples`, `Frequency`, `Ranges`, `Value`,
`Trigger`, `NumberOfElements`) are carried by the copy items:

```marte
copy DecimateGAM {
  DDB1::PXI6259_Status_SAMPLES { Ranges = { { @dec_index, @dec_index } } } -> DDB1::PXI6259_Status
  …
}
```

`EPICSOutputGAM` stays as written: its input and output lists differ in
length (67 inputs, 69 outputs), so it cannot be expressed as 1:1 copy pairs.

One pipeline groups the two unconditional ADC publishers, binding the 19
source signals once:

```marte
gam AdcPublication {
  in Time
  in GY_APS_V_MEAS
  …
  copy PushADCtoDAN { Time -> FastAnalogDAN::Time  … }
  copy PushADCtoEpics { … }
}
+AdcPublication_ = { Class = AdcPublication  Time = DDB4::Time  … }
```

`PushADCtoUDP` stays a copy group inside `#if @udp_streamer`: pipeline
instances inside conditional blocks are not expanded (their generated
definitions are conditional while the references are not).

## Correctness

Both projects check cleanly and are **semantically identical**:

```bash
mdt check -P examples/big_project        # No issues found.
mdt check -P examples/big_project_sugar  # No issues found.

mdt build -P examples/big_project       -o /tmp/orig.marte
mdt build -P examples/big_project_sugar -o /tmp/refac.marte
```

`TestBigProjectRefactorEquivalence` (test/big_project_refactor_test.go)
parses both outputs and compares the semantic model: every object and
class, all fields, every signal entry (name, DataSource, Alias and options)
and the ordered thread function lists. Field order and alias quoting are
ignored because they are not semantic; `diff` between the two outputs shows
only those.
