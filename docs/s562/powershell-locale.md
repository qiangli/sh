# PowerShell startup environment: Sprint 562

Story `391b8ce50a58`. Base: verified combined runtime
`f1a41ac0f3dc21a441042bec8bd95cec4a4dbcc9`. CSharp assembly owner confirmed
`powerShellLaunchEnvironment` is disjoint (mb:12287). No CSharp assembly fields
or environment serializer fields changed.

Planning admits exactly `LANG`, `LC_ALL`, `LC_CTYPE`, `TERM`, and the existing
`DOTNET_SYSTEM_GLOBALIZATION_INVARIANT` control, preserving empty versus absent.
Explicit invariant settings take precedence over provisioner defaults. These
values participate in the ordinary immutable plan fingerprint.

Compiled PowerShell initialization clones its embedded plan and binds explicit
execution inputs for those same five keys once. Absent keys retain embedded
defaults. Changed effective environment bytes derive a new fingerprint from the
build fingerprint; source manifests are not re-read on deployment. Worker
restarts reuse that snapshot. Other runtime paths and arbitrary ambient keys
are excluded from this override. Interpreted execution uses its request snapshot.

## Evidence and bounded gate

The local baseline dropped all three locale/terminal variables. With de_DE,
native pwsh printed `14.07.2020 13:24:35`, while interpreted/compiled execution
printed the US date. Explicit-empty discovery tests also failed before the fix.
The gate tests en_US and de_DE with intentionally different build settings
(`C`, `xterm`, invariant `1`) and execution settings (selected locale, `dumb`,
invariant `0`). Native, interpreted and compiled bytes must agree.

`lower/testdata/powershell-locale` contains the exact **16** recipe cases,
including arrays L564, copied unchanged from the manager's PS36 subset. The
receipt binds original fence/source SHA256, upstream pin
`a3de8f22552170e70852470d46cd52cd9ca471ec`, declared environment, native status,
stdout and stderr (three historical observations each). The native `oracle.ps1`
is copied unchanged from the corpus harness. Tests verify both source hashes,
fresh native bytes against the immutable receipts/goldens, and both fence modes
against the fresh oracle. Advanced parameters L126 intentionally exits 1; the
gate compares its error bytes and exit status rather than treating it as a pass
with exit 0. No golden, recipe, or oracle normalization was introduced.

Use an authenticated Go 1.27.2 and PowerShell 7.6.6, isolated Go caches, and a new
evidence directory. Linux acceptance includes all 16 recipes:

```sh
python3 docs/s562/verify-powershell-locale.py --go /path/to/go \
  --pwsh /path/to/pwsh --evidence /new/evidence --corpus \
  --expected-commit SOURCE_COMMIT
```

Native Windows relevant regression uses the archive manifest and explicit tools:

```powershell
./docs/s562/verify-powershell-locale.ps1 -Go C:/path/go.exe `
  -Pwsh C:/path/pwsh.exe -Evidence C:/new/evidence -ExpectedCommit SOURCE_COMMIT
```

Both gates require named test and package PASS events, reject skips/failures,
and preserve raw JSON events, stderr, status, provider hashes and source identity.
Archive gates validate every source file against a SHA256 manifest. The Windows
regression covers case-insensitive admission, explicit-empty snapshots,
provisioner precedence, immutability/fingerprints, and both-mode native date/env
parity. Windows compilation alone is not native execution proof. Full PS36 and
cross-platform manager acceptance remain separate from these bounded gates.

## Delivery receipt

Implementation commit: `e62d799641169b91f68e72a8210277837e9f4da9`.
The exact clean-commit local gate passed 8 planning and 38 lower test/subtest
PASS events, with zero skips/failures. Four PowerShell/CSharp controls passed;
focused race tests and Windows test-binary cross-compilation passed.
[The receipt](powershell-locale-receipt.json) records tool hashes and raw evidence
hashes. The local evidence archive is staged as
`linux-evidence/ps-locale-worker22/e62d7996-local-evidence.tar.gz`.

Source manifests were checked after extraction at both manager staging paths:

- Linux: `linux-stage/ps-locale-worker22/candidate-e62d7996`
- Windows: `windows-stage/ps-locale-worker22/candidate-e62d7996`

Role-relative labels resolve through the private manager handoff.
No remote acceptance execution is claimed. The manager runs the staged gates
using its authenticated providers and isolated caches before acceptance.

Additional baseline limitation: `TestEnvironmentLiteralCarriesEveryField`
panics on f1's `NativeModules []NativeModuleUnit`, because that pre-existing
reflection test assumes every slice is `[]string`. Both relevant files were
checked in the f1 commit; this change does not alter that test or serialization.
The manager was notified separately.

## C# startup follow-up

C# runs inside the same PowerShell provider. Its compiled initialization now
uses the same five-key execution snapshot as PowerShell. The override changes
neither assembly resolution nor graph/native assets. Tests run both language
plans through absent, explicit-empty, override, immutability and fingerprint
controls. The C# culture fixture compares native, interpreted and compiled
bytes with build invariant `1` and execution invariant `0`, including an empty
`TERM`, en_US/de_DE formatting and an explicit fr-FR culture lookup.

The exact C#510 `fundamentals/strings-interpolation` fence, recipe and oracle
are retained in `lower/testdata/csharp-locale`, from dotnet/docs pin
`20f8ab0cae298fa9447eb4b48ae0fb3665741c26` (MIT). Its fence SHA256 is
`f7e1506bb34c6844fec57c2afaff1a21fdabb47f179f3f1776d3ae3ffa4d8b38`.
The regression extracts the native body and invokes the original entrypoint;
it compares raw bytes with both execution modes and, on Unix, the unchanged
corpus oracle. Windows compares raw native bytes to both modes without applying
Unix newline normalization. Both gate scripts require the new C# tests.

Reviewed combined base `d441ce39` reproduces the synthetic culture failure in
both locale cases: interpreted succeeds, compiled reports
`CultureNotFoundException`. The follow-up is staged as a bounded patch against
that base for manager integration; the worker branch retains its prior f1-based
commits. Public evidence labels remain role-relative. Remote corpus acceptance
is still the manager's gate.
