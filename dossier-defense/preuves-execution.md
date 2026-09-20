# Preuves d'exécution — « le test échoue-t-il vraiment sans la correction ? »

Un test qui passe dans les deux cas ne prouve rien. Pour chaque PR, la vérification consiste à **garder les nouveaux tests** et à **remettre le code d'origine**, puis à lancer les tests : ils doivent échouer.

Vérification faite le 20 septembre 2026, sous Windows 11 avec Go 1.27, sur des copies de travail jetables. Les noms ci-dessous sont ceux réellement affichés par `go test`.

## Résultat par PR

| PR | Verdict | Tests qui échouent sans la correction |
|---|---|---|
| #1470 | ✅ Prouvé | `TestGetCommitMessage_IgnoresGitStderr`, `TestAmbiguousRefWarningDoesNotPolluteParsedOutput` (sous-tests `runner` et `direct`), `TestEnumerate_RunnerIgnoresGitStderr` |
| #1471 | ✅ Prouvé | `TestGetDiffGitignoreStarStopsAtSlash`, `TestMatchGitignoreBodyStarStopsAtSlash`, `TestGetDiffGitignoreMiddleSlashIsAnchored`, `TestGetDiffHonoursNestedGitignoreNegation`, `TestGetDiffGitignoreHonoursInfoExclude`, `TestGetDiffKeepsUntrackedNameWithLeadingSpace` |
| #1472 | ✅ Prouvé | `TestCommitDiffSeparatesQuotedPath`, `TestWorkspaceDiffPathContainingSpaceB`, `TestParseDiffText_QuotedHeaderStartsNewFile`, `TestParseDiffText_QuotedRename`, `TestParseDiffText_PathContainingSpaceB` |
| #1473 | ✅ Prouvé | `TestPartitionMessages_EverythingFits`, `…KeepsMostRecentRound`, `…ChargesFrozenZone`, `TestAddNextMessage_CompressionAccountsForFrozenZone`, `TestAddNextMessage_KeepsCurrentRound` |
| #1474 | ✅ Prouvé | `TestExecuteGroupSubtask_LaterRoundStopKeepsCompletion` |
| #1502 | ✅ Prouvé | `TestDispatchSubtasks_RenamedFileCommentOnOldPath`, `TestExecuteToolCall_CodeCommentRenamedOldPath` |
| #1503 | ✅ Prouvé | `TestExecuteGroupSubtask_PathlessCommentInGroup/unlocatable_is_dropped`, `TestExecuteToolCall_CodeCommentGroupKeyFallbackDropped` |
| #1475 | ✅ Prouvé | `TestDiscoverRepos_FindsUNCRepoSessions` |
| #1476 | ✅ Prouvé | `TestDispatchSubtasks_BundleSplitAccountsForPromptOverhead` : « got 1 groups, want 2 », puis « prompt tokens (8223) exceed 80% of max_tokens(10000) » |
| #1477 | ✅ Prouvé | `TestNestedBracePatterns` : `IsUserExcluded("src/a/x.go") = false, want true`, idem pour `IsUserIncluded` et `Resolve` |
| #1478 | ✅ Prouvé | `TestDispatchSubtasks_ResumeKeepsReusedWhenAllFreshFail` |
| #1479 | ✅ Prouvé | `TestResolveEndpoint_AnthropicV1URLNormalized` (6 sous-tests), `TestResolveEndpoint_OCREnvAnthropicV1URLReachesMessagesPath` |
| #1480 | ✅ Prouvé | `TestResolveEndpoint_EnvExtraHeaderOverridesConfigCaseInsensitively`, `…OverrideOnTheWire`, `TestResolveEndpoint_ConfigFileExtraHeadersReservedRejected` (3 sous-tests) |
| #1481 | ✅ Prouvé | `TestSetConfigValueProviderRejectsEmptyName` (3 sous-tests) |
| #1504 | ✅ Prouvé | `TestResolveConfig`, 6 sous-tests de désactivation (`"0"`, `"false"`, `"FALSE"`, `"no"`, `"off"`, `" 0 "`) |
| #1482 | ✅ Prouvé | `TestOutputJSONWithWarnings_FailedRunCommentsNotNull`, `TestSarifArtifactURI_EncodesPathSegments` (3 sous-tests), `TestTruncateText` (2 sous-tests) |
| #1483 | ✅ Prouvé | `TestShellCommand_CmdLine`, `TestShellCommand_QuotedScripts` (2 sous-tests) |
| #1430 | ✅ Prouvé | `TestConfigureProcessGroup_CancelKillsGrandchild` : sans la correction, l'attente dure 33 s au lieu de se terminer, et le processus petit-enfant survit |
| #1431 | ✅ Prouvé | `TestFileFind_NonASCIIPath` |
| #1505 | ✅ Prouvé | `TestGitGrep_CRLFFileHasNoTrailingCR` |
| #1432 | ✅ Prouvé | `TestResolveComment_DeletedSnippetSpanningBlankLine` |
| #1433 | ✅ Prouvé | `TestEmitRunResult_RecordsNoRunMetrics` |
| #1434 | ⚠️ Garde-fou | Les tests **passent aussi sans la correction**. Voir ci-dessous. |
| #1435 | ➖ Sans objet | Suppression de code mort : aucun test ajouté, 162 lignes retirées |

**22 PR sur 24 sont prouvées par un test qui échoue sans elles.** Après le découpage des trois pull requests groupées, chaque morceau a été revérifié séparément : son test échoue bien quand on retire sa seule correction.

## Les deux exceptions, en toute transparence

**#1434, écriture atomique de `config.json`.** Ses trois tests (aucun fichier temporaire laissé, nettoyage après un échec, écriture à travers un lien symbolique) passent aussi avec l'ancien code. C'est normal : ils protègent le **nouveau** chemin de code contre une régression future. Le scénario réellement corrigé, une coupure de courant en pleine écriture, ne se simule pas raisonnablement dans un test unitaire. C'est écrit tel quel dans la description de la PR. En revanche, une régression **a** été prouvée et corrigée pendant la relecture : ma première version cassait le cas du lien symbolique vers un fichier inexistant, et le test correspondant échouait bien sur cette version.

**#1435, suppression de code mort.** Il n'y a rien à prouver par un test : la démonstration est que les quatre fonctions ne sont appelées que par leurs propres tests, et que le projet compile et passe tous ses tests une fois qu'elles sont retirées.

## Détail de la méthode

Pour cinq PR, la vérification a demandé un traitement particulier : leurs tests appellent une fonction que la correction introduit, donc remettre simplement le code d'origine empêche la compilation, ce qui ne prouve rien. Dans ces cas, seuls les fichiers **modifiés** ont été remis dans leur état d'origine, en gardant les fonctions nouvelles, et les tests unitaires de ces fonctions ont été écartés :

| PR | Traitement |
|---|---|
| #1471 | Seul `internal/diff/git.go` remis à l'origine ; les tests de la fonction interne `gitIgnoredPaths` écartés |
| #1472 | Seul `internal/diff/parser.go` remis à l'origine ; `diffheader.go` conservé |
| #1476 | Le compteur de tokens remplacé par `nil`, ce qui est exactement le repli documenté vers l'ancien calcul |
| #1477 | `system_rules.go` remis à l'origine, et la copie de `expandBraces` déplacée dans les tests retirée |
| #1433 | `internal/telemetry/testing.go` conservé, les trois fichiers porteurs du défaut remis à l'origine |
