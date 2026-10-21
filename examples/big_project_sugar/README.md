# big_project, refactored with copy groups

This is the reference `examples/big_project` rewritten with the experimental
copy sugar (see [docs/COPY_SUGAR.md](../../docs/COPY_SUGAR.md)). The original
project is kept unchanged next to it.

## What changed

Four IOGAMs whose signal entries only carry `DataSource` were replaced by
named copy groups with the same GAM names, in the same position and pair
order:

| File | GAM | Pairs |
|---|---|---|
| `thread1/thread_data_broker.marte` | `EPICSThSyncGAM` | 8 |
| `thread3/datasync.marte` | `AsyncIOGAM` | 4 |
| `thread4/adc_2_dan.marte` | `PushADCtoUDP` (inside `#if @udp_streamer`) | 7 |
| `thread4/adc_2_dan.marte` | `PushADCtoDAN` | 18 |

Example:

```marte
copy AsyncIOGAM {
  WGAsyncBridge::FHPS_A_RU -> DDB3::FHPS_RU
  Th1Bridge::CCPS_IN_OPERATION -> DDB3::CCPS_IN_OPERATION
  Th1Bridge::MCPS_RU_COMPLETED -> DDB3::MCPS_RU_COMPLETED
  Th1Bridge::MCPS_RD_COMPLETED -> DDB3::SCM_RU
}
```

Every IOGAM whose entries carry per-signal options (`Samples`, `Frequency`,
`Ranges`, `Value`, `Trigger`, `NumberOfElements`, `Alias`) was left as
written: the copy syntax has no way to express those options yet, and
dropping them would change runtime behaviour.

## Correctness

Both projects build to **byte-identical** configurations:

```bash
mdt build -P examples/big_project       -o /tmp/orig.marte
mdt build -P examples/big_project_sugar -o /tmp/refac.marte
diff /tmp/orig.marte /tmp/refac.marte    # no output

mdt check -P examples/big_project        # No issues found.
mdt check -P examples/big_project_sugar  # No issues found.
```

`TestBigProjectRefactorEquivalence` (test/big_project_refactor_test.go)
performs the same comparison in CI.
