# Dossier de défense — Groupe A

> **Note de vérification.** Les messages d'échec cités dans ce dossier ont été reconstitués à partir du code des tests. Les résultats réellement observés en exécutant les tests sans leur correction sont consignés dans `preuves-execution.md` : citez ceux-là devant un mainteneur.


Projet : `alibaba/open-code-review` (Go). Cinq pull requests : #1470, #1471, #1472, #1483, #1430.

Ce dossier explique chaque changement pour que vous puissiez le défendre avec vos mots, sans avoir écrit le code. Les identifiants de code restent en anglais entre backticks. Les numéros de ligne renvoient au fichier tel qu'il est **sur la branche de la PR**.

Quelques notions qui reviennent partout, expliquées une seule fois :

- **stdout / stderr** : un programme lancé en sous-processus écrit sur deux canaux séparés. `stdout` porte le résultat (un SHA, une liste de fichiers, un diff). `stderr` porte les messages destinés à l'humain (avertissements, traces). En Go, `cmd.CombinedOutput()` fusionne les deux dans un seul tampon, `cmd.Output()` ne rend que `stdout`.
- **hunk** : dans un diff, un bloc de lignes modifiées à l'intérieur d'un fichier, introduit par une ligne `@@ ... @@`.
- **en-tête `diff --git`** : la ligne qui ouvre le bloc d'un fichier dans un diff unifié, de la forme `diff --git a/ancien b/nouveau`. C'est elle qui dit au lecteur (et au parseur) « un nouveau fichier commence ici ».

---

## PR #1470 — Ne lire que la sortie standard de git

**Branche** : `fix/git-stderr-pollution` · **Fichiers touchés** : `cmd/opencodereview/git.go`, `internal/diff/git.go`, `internal/scan/provider.go`, plus trois fichiers de test (`cmd/opencodereview/background_file_test.go`, `internal/diff/git_stderr_test.go` (nouveau), `internal/scan/provider_test.go`).

### 1. Le problème en une phrase

Trois fonctions lisent la sortie **fusionnée** de `git` (stdout + stderr), si bien qu'un simple avertissement de git est interprété comme une donnée : un SHA, un nom de fichier ou un message de commit.

### 2. Pourquoi ça compte

Deux situations réelles, reproduites par les tests de la PR.

- Un dépôt où une branche et une étiquette portent le même nom, par exemple `main`. Git accepte la commande mais écrit `warning: refname 'main' is ambiguous.` sur stderr. Cet avertissement devient la première ligne lue, donc la « base de comparaison » de la revue. La revue échoue avec une erreur qui ne parle ni de branche ni d'étiquette.
- Un utilisateur qui a `GIT_TRACE=1` dans son environnement (la variable de débogage standard de git : elle fait écrire des lignes de trace sur stderr). Le message de commit utilisé comme contexte de la revue se retrouve préfixé de lignes de trace, et la liste des fichiers à scanner se retrouve polluée par des entrées qui ne sont pas des fichiers.

Dans les deux cas l'utilisateur ne voit pas de lien avec git : soit la revue échoue, soit elle porte sur de mauvaises données.

### 3. Comment le bug se produit

1. `internal/diff/git.go:731`, `runGit` : sur le chemin direct elle appelait `cmd.CombinedOutput()`, et sur le chemin « runner » elle appelait `p.runner.Run(...)`, dont la documentation dans `internal/gitcmd/runner.go` dit explicitement « returns the combined stdout+stderr output ». Les deux chemins mélangent donc les canaux.
2. Tous les appelants de `runGit` traitent la sortie comme une donnée :
   - `computeMergeBase` (`internal/diff/git.go:458`) fait un `strings.TrimSpace` de **toute** la sortie et la rend comme base de comparaison ;
   - `resolveCommit` (`:580`) et `commitParents` (`:595`) passent par `firstLine` (`:609`), qui rend la **première ligne non vide**. Or un avertissement de git est écrit avant le résultat : c'est lui la première ligne ;
   - `RemoteIdentity` (`:474`) fait de même sur l'URL de `origin`.
3. `cmd/opencodereview/git.go:27`, `getCommitMessage` : elle appelait `runGitCmd`, la variante à sortie fusionnée, pour lire `git log -1 --format=%B`. Le message de commit sert de contexte à la revue ; tout ce que git écrit sur stderr s'y ajoute.
4. `internal/scan/provider.go:245`, `gitLs` : la commande est `git ls-files -z`, qui sépare les noms de fichiers par un octet NUL. Le chemin direct utilisait déjà `cmd.Output()`, **avec un commentaire qui expliquait pourquoi**. Le chemin « runner », juste à côté, utilisait `runner.Run` : sortie fusionnée. Les octets de stderr entrent alors dans le découpage des noms de fichiers.

Autrement dit, le projet avait déjà identifié le problème à un endroit et l'avait corrigé localement, sans propager la règle.

### 4. Ce que fait la correction

Elle bascule ces trois points sur une lecture de stdout seul :

- `runGit` utilise `cmd.Output()` et `p.runner.Output(...)` ;
- `getCommitMessage` utilise `runGitCmdStdout` ;
- le chemin « runner » de `gitLs` utilise `p.runner.Output(...)`, et le commentaire qui n'expliquait que le chemin direct est remonté pour couvrir les deux.

Aucune API nouvelle n'est créée : `runGitCmdStdout` existait déjà dans `cmd/opencodereview/git.go:21` et `Runner.Output` existait déjà dans `internal/gitcmd/runner.go`. La PR se contente de les employer là où il le fallait.

Un commentaire est ajouté au-dessus de `runGit` pour fixer la règle : cette fonction rend stdout seul parce que tous ses appelants analysent la sortie comme une donnée et la jettent en cas d'erreur.

**Pourquoi cette approche plutôt qu'une autre.** Le dépôt possède déjà une troisième variante, `runGitSplit`, qui garde stdout et stderr séparés et rend les deux. Elle est utilisée par les appelants qui ont besoin du texte de stderr pour construire un message d'erreur lisible (`GetDiffSet`, `untrackedFilesList`). La PR ne convertit pas `runGit` en `runGitSplit` parce que ses appelants n'utilisent jamais stderr : ils rendent `""` ou `nil` en cas d'échec. Ajouter une troisième valeur de retour à chacun aurait alourdi le code sans rien apporter.

### 5. La preuve

Trois tests, chacun rend à git une occasion d'écrire sur stderr **sans échouer**.

- `internal/diff/git_stderr_test.go`, `TestAmbiguousRefWarningDoesNotPolluteParsedOutput`. Il construit un dépôt avec une branche `main` **et** une étiquette `main`, puis une branche `feature`. Il vérifie que `MergeBase` et `ResolveInput().ResolvedBase` correspondent à l'expression régulière `^[0-9a-f]{40}$` — un SHA nu, rien d'autre — et que `GetDiff` rend exactement un diff. Le test est joué deux fois, avec un `gitcmd.Runner` et sans, parce que les deux chemins avaient le même défaut. Sur `main`, la valeur rendue contient la ligne `warning: refname 'main' is ambiguous.` : la comparaison à l'expression régulière échoue, et le `git diff` construit avec cette base échoue à son tour.
- `cmd/opencodereview/background_file_test.go`, `TestGetCommitMessage_IgnoresGitStderr`. Il crée un dépôt avec un commit dont le message est `real message`, pose `GIT_TRACE=1` avec `t.Setenv`, puis exige `getCommitMessage == "real message"`. Sur `main`, la valeur rendue commence par les lignes de trace de git.
- `internal/scan/provider_test.go`, `TestEnumerate_RunnerIgnoresGitStderr`. Même principe, sur le chemin « runner » de `gitLs` : un dépôt avec un seul fichier `main.go`, `GIT_TRACE=1`, et l'assertion qu'`Enumerate` rend exactement un élément nommé `main.go`. En cas d'échec il affiche la liste complète des chemins obtenus, donc on voit directement les fragments de trace pris pour des fichiers.

Pourquoi ces tests prouvent bien **ce** défaut : la seule chose qu'ils changent est ce que git écrit sur stderr. Les commandes, le dépôt et les arguments sont identiques. Si le résultat change, c'est que stderr est lu.

### 6. Limites et effets de bord

- La PR ne touche que trois points d'appel. `runGitCmd` (sortie fusionnée) reste en place pour ses autres appelants ; la PR ne prétend pas avoir audité tout le dépôt.
- Pour ces trois appels, stderr n'est plus fusionné dans la valeur de retour. Ce n'est pas une perte d'information pour le diagnostic : en Go, `cmd.Output()` capture stderr dans le champ `Stderr` de l'`*exec.ExitError` rendu. Et ces appelants jetaient déjà la sortie en cas d'erreur.
- Aucun changement de comportement visible pour un utilisateur dont le git est silencieux — c'est-à-dire le cas nominal. Le changement ne se voit que dans les cas où la revue était déjà cassée.

### 7. Questions probables d'un mainteneur

**« Pourquoi ne pas tout faire passer par `runGitSplit`, puisqu'elle existe ? »**
Parce qu'aucun appelant de `runGit` n'utilise stderr. `computeMergeBase`, `resolveCommit`, `commitParents` et `RemoteIdentity` rendent une chaîne vide ou `nil` en cas d'erreur. Leur ajouter une troisième valeur de retour à propager aurait touché plus de code pour un bénéfice nul. `runGitSplit` reste utilisée là où le texte de stderr sert à composer un message d'erreur.

**« `GIT_TRACE=1` dans un test, n'est-ce pas un peu artificiel ? »**
C'est un déclencheur commode, pas le scénario principal. Le scénario réel est le test sur la référence ambiguë : une branche et une étiquette homonymes suffisent à faire parler git, et ça arrive dans des dépôts qui taguent leurs releases. `GIT_TRACE` est simplement la façon la plus fiable de faire écrire git sur stderr dans un test, sans dépendre de la version de git ni de la langue du message.

**« On ne perd pas le message d'erreur de git ? »**
Non. `cmd.Output()` place stderr dans `ExitError.Stderr`, donc l'information reste accessible si on veut la remonter un jour. Et de toute façon, ces appelants-là ne la regardaient pas : ils convertissaient toute erreur en résultat vide.

**« Pourquoi ne pas filtrer les lignes qui commencent par `warning:` ? »**
Parce que ce filtre serait faux. Les messages de git dépendent de sa version et peuvent être traduits, un hook ou un `credential helper` peut écrire n'importe quoi sur stderr, et rien ne garantit qu'une ligne de données ne commence pas par le même mot. La séparation des canaux est la garantie que le système d'exploitation offre déjà ; il suffit de s'en servir.

**« Pourquoi toucher `internal/scan` alors que ce chemin était déjà correct ? »**
Il ne l'était qu'à moitié. Dans la même fonction, `gitLs`, la branche sans `Runner` utilisait `Output` et portait un commentaire d'explication, tandis que la branche avec `Runner`, quatre lignes plus haut, utilisait la sortie fusionnée. C'est le même bug, au même endroit, sur l'autre chemin. Le commentaire a été remonté pour qu'il couvre les deux.

**« Ce changement peut-il casser un cas où la sortie fusionnée était voulue ? »**
Pour ces appels, non : ils analysent un SHA, une liste de parents, une URL ou une liste de noms de fichiers. Aucun n'a de raison de vouloir du texte destiné à l'humain dans son résultat. Les tests existants de `internal/diff` et `internal/scan` continuent de passer, ce qui couvre le comportement nominal.

---

## PR #1471 — Laisser git décider quels fichiers sont ignorés

**Branche** : `fix/gitignore-matching` · **Fichiers touchés** : `internal/diff/git.go`, `internal/gitcmd/runner.go`, plus les tests (`internal/diff/gitignore_git_test.go` (nouveau), `internal/gitcmd/runner_test.go`).

### 1. Le problème en une phrase

Le projet réimplémente les règles de `.gitignore` à la main, et cette réimplémentation se trompe : sur Windows l'étoile `*` traverse les `/`, et partout elle ignore les `.gitignore` imbriqués, leurs négations, `.git/info/exclude` et `core.excludesFile`.

### 2. Pourquoi ça compte

Le filtre décide quels fichiers modifiés entrent dans la revue. Quand il se trompe, la revue passe à côté de fichiers **sans le dire**.

Exemple tiré du test `TestGetDiffGitignoreStarStopsAtSlash` : un `.gitignore` racine contenant `/*.go` et `docs/*.md`. Sous Windows, `/*.go` faisait disparaître `pkg/x.go` et `pkg/new.go` de la revue, et `docs/*.md` faisait disparaître `docs/a/b.md`. Un dépôt Go dont le `.gitignore` racine contient `/*.go` (une ligne parfaitement ordinaire pour exclure les fichiers Go générés à la racine) voyait donc **tout son code Go** exclu de la revue sur Windows, et pas sur Linux.

Autre exemple, tiré de `TestGetDiffHonoursNestedGitignoreNegation` : un `.gitignore` racine `*.gen.go` plus un `api/.gitignore` contenant `!*.gen.go`. Git suit ce dernier et considère `api/types.gen.go` comme suivi ; l'ancien filtre, qui ne lisait que le fichier racine, le retirait de la revue.

Et `TestGetDiffKeepsUntrackedNameWithLeadingSpace` : un fichier non suivi nommé ` lead.go` (avec une espace initiale) disparaissait purement et simplement, parce que son nom était rogné avant d'être relu sur le disque.

### 3. Comment le bug se produit

**Le `*` qui traverse les `/`.** En Go, `filepath.Match` définit `*` comme « n'importe quelle suite de caractères **qui ne sont pas le séparateur** ». Le séparateur, c'est `filepath.Separator`, qui vaut `/` sous Unix et `\` sous Windows. Or les chemins manipulés ici viennent de git : ils sont **toujours** écrits avec des `/`. Sous Windows, `/` n'est donc pas un séparateur pour `filepath.Match`, et `*` le traverse allègrement.

1. `partitionDiffs` (`internal/diff/git.go`, avant la PR) chargeait le `.gitignore` racine avec `loadGitignorePatterns` puis appelait `isPathExcluded` (`:310`) pour chaque fichier modifié.
2. `isPathExcluded` délègue à `matchGitignoreBody` (`:359`), qui appelait `filepath.Match`.
3. Sous Windows, `filepath.Match("*.go", "pkg/x.go")` rend `true`, donc le motif ancré `/*.go`, censé ne viser que la racine, attrape tout l'arbre. Même chose pour `filepath.Match("docs/*.md", "docs/a/b.md")`.
4. Sous Linux, les mêmes appels rendent `false` : le bug est **spécifique à Windows**, ce qui explique qu'il ait pu passer inaperçu en intégration continue.

**Les règles que le filtre ne voyait pas.** `loadGitignorePatterns` lit un seul fichier : `.gitignore` à la racine du dépôt. Git, lui, combine les `.gitignore` de chaque répertoire traversé (avec leurs négations `!`), `.git/info/exclude` (les exclusions locales non versionnées) et `core.excludesFile` (le fichier d'exclusions global de l'utilisateur). Tout cela était invisible.

**Le nom rogné.** `untrackedFilesList` (avant la PR) lançait `git ls-files --others --exclude-standard` sans `-z`, découpait la sortie sur `\n` et appliquait `strings.TrimSpace` à chaque ligne. Un fichier nommé ` lead.go` devenait `lead.go` ; la lecture ultérieure du fichier échouait, et le fichier sortait de la revue sans message.

### 4. Ce que fait la correction

**On demande à git.** `partitionDiffs` (`:444`) appelle désormais `ignoredPathsFilter` (`:490`), qui appelle `gitIgnoredPaths` (`:509`). Cette dernière lance **un seul** `git check-ignore --no-index -z --stdin` et lui donne tous les chemins candidats d'un coup.

Détails qui comptent :

- Les chemins sont envoyés sur **l'entrée standard**, séparés par des octets NUL, pas en arguments. Cela évite à la fois la limite de longueur de ligne de commande et tout problème d'échappement de noms exotiques.
- `--no-index` demande à git de ne pas tenir compte de l'index. Sans cette option, un fichier déjà suivi serait déclaré « non ignoré » d'office. Le projet veut appliquer les règles d'exclusion aussi aux fichiers suivis — c'était déjà son comportement avant la PR, avec le filtre maison.
- Un nom qui commence par `:` est préfixé de `./`, parce que `check-ignore` refuse `--literal-pathspecs` et lirait le `:` comme de la « magie de pathspec » (une syntaxe git du genre `:(top)`, `:!`). Le préfixe `./` neutralise cette lecture, et il est retiré de la réponse.
- Le code de sortie 1 de `check-ignore` signifie « aucun des chemins n'est ignoré », pas « échec ». Il est donc traité comme un succès ; toute autre erreur est remontée.

**Un repli, mais explicite.** Si git ne peut pas répondre — typiquement hors d'un dépôt — `ignoredPathsFilter` écrit un avertissement sur stderr (`[ocr] WARNING: ...; falling back to root .gitignore matching`) et revient à l'ancien filtre maison. Un contexte annulé, lui, est remonté comme erreur et **pas** masqué par le repli : sinon une annulation se déguiserait en « aucun fichier ignoré ».

**L'ancien filtre est quand même réparé.** `matchGitignoreBody` passe de `filepath.Match` à `path.Match`, qui utilise toujours `/` comme séparateur quel que soit le système. Ce n'est pas redondant : le filtre maison reste utilisé par `internal/scan/provider.go` et `internal/tool/file_find.go`, via les fonctions exportées `diff.LoadGitignorePatterns` et `diff.IsPathExcluded` (`internal/diff/gitignore.go`). Ces deux consommateurs bénéficient donc directement de la correction du `*`.

**Les fichiers non suivis.** `untrackedFilesList` (`:788`) ajoute `-z` et découpe sur NUL, donc les noms arrivent tels quels. Le second passage du filtre maison est supprimé : `--exclude-standard` a déjà appliqué **toutes** les règles d'exclusion de git. Il ne reste à vérifier que la liste noire de répertoires propre au projet (`isProviderDirExcluded` : `vendor/`, `node_modules/`, `.git/`, `target/`, etc.), et ces fichiers-là ne sont même pas lus, parce qu'un arbre vendorisé peut être volumineux.

**Plomberie ajoutée.** `Runner.RunSplitStdin` dans `internal/gitcmd/runner.go` et `Provider.runGitSplitStdin` (`internal/diff/git.go:840`) : ce sont les variantes de `RunSplit` / `runGitSplit` qui acceptent une entrée standard. `runGitSplit` devient un simple appel à `runGitSplitStdin(ctx, nil, ...)`, donc son comportement ne change pas.

**Alternatives écartées.** Continuer à améliorer le filtre maison n'aurait pas suffi : même corrigé sur le `*`, il ne lit toujours qu'un seul fichier et ne peut pas reproduire l'ordre de résolution de git sur des `.gitignore` imbriqués. Faire un `check-ignore` par fichier aurait multiplié les sous-processus ; d'où l'appel unique avec `--stdin`.

### 5. La preuve

Huit tests dans `internal/diff/gitignore_git_test.go`, tous sur un vrai dépôt git, plus un test de plomberie.

- `TestGetDiffGitignoreStarStopsAtSlash` : `.gitignore` = `/*.go`, `docs/*.md`, `build/`. Attendu : `docs/a/b.md`, `pkg/new.go`, `pkg/x.go`. Sur `main` **sous Windows**, la liste obtenue ne contient ni les `.go` de `pkg/` ni `docs/a/b.md`. Sous Linux ce test passait déjà : c'est la démonstration que le bug est lié au séparateur.
- `TestMatchGitignoreBodyStarStopsAtSlash` : la même règle testée directement sur le filtre maison, cinq cas dont `("sub/x.go", "/*.go") → false`. C'est ce test qui couvre `internal/scan` et `file_find`.
- `TestGetDiffGitignoreMiddleSlashIsAnchored` : `config/local.json` dans le `.gitignore` ne doit exclure que le fichier à la racine, pas `pkg/config/local.json`. C'est une règle git peu connue : dès qu'un motif contient un `/` ailleurs qu'à la fin, il est relatif au `.gitignore` qui le contient.
- `TestGetDiffHonoursNestedGitignoreNegation` : `.gitignore` racine `*.gen.go`, `api/.gitignore` `!*.gen.go`. Seul `api/types.gen.go` doit rester.
- `TestGetDiffGitignoreHonoursInfoExclude` : `.git/info/exclude` contenant `*.cfg` doit compter, y compris pour un fichier **suivi**.
- `TestGetDiffKeepsUntrackedNameWithLeadingSpace` : le fichier ` lead.go` doit apparaître avec son nom exact et une insertion comptée. Sur `main` il disparaît.
- `TestGitIgnoredPathsTakesNamesLiterally` : un seul appel `check-ignore` avec six noms hostiles (`:(top)x.log`, `:keep.go`, `*.go`, ` sp.log`, `out/a.go`, `src/a.go`) et vérification du verdict de chacun. Il vérifie aussi le cas « aucun ignoré » (code de sortie 1) et le chemin sans `Runner`.
- `TestIgnoredPathsFilterFallsBackWhenGitFails` : dans un répertoire qui n'est pas un dépôt, le repli s'active et applique quand même `*.log`.
- `TestIgnoredPathsFilterReturnsCancellation` : un contexte déjà annulé doit produire une erreur, pas un verdict silencieux.
- `internal/gitcmd/runner_test.go`, `TestRunner_RunSplitStdin` : la nouvelle méthode transmet bien l'entrée standard (via `git hash-object --stdin`, dont le résultat est comparé au SHA du blob correspondant) et respecte l'annulation du contexte.

Les tests écrivent d'abord les fichiers, puis font `git add -f .` pour les enregistrer **même s'ils sont ignorés** : c'est ce qui permet de tester précisément le cas « fichier suivi mais couvert par les règles d'exclusion ».

### 6. Limites et effets de bord

- **Un sous-processus git de plus par revue.** Il est unique et groupé, mais il existe. Sur un dépôt sans changement il est évité : `ignoredPathsFilter` rend immédiatement un prédicat « rien n'est ignoré » quand la liste est vide.
- **Le périmètre de la revue change pour certains utilisateurs.** Un dépôt qui utilise des `.gitignore` imbriqués, `.git/info/exclude` ou un fichier d'exclusions global verra désormais ces règles appliquées. C'est le comportement voulu — le filtre était censé imiter git — mais des fichiers qui entraient dans la revue peuvent en sortir, et inversement pour les négations imbriquées.
- **Le filtre maison reste une approximation** là où il sert encore (`internal/scan`, `internal/tool/file_find`). La PR corrige son bug de séparateur mais ne le remplace pas par `check-ignore` dans ces deux consommateurs.
- **Le repli est silencieux dans son verdict, pas dans son déclenchement** : il écrit un avertissement, mais l'utilisateur doit le lire.
- **Dépendance à `git check-ignore`** : la commande existe depuis git 1.8.2 et l'option `--stdin` y est présente, mais la PR ne pose pas de version minimale explicite.
- Le comportement sous Unix pour le cas `/*.go` était déjà correct : cette partie-là de la PR ne change rien pour les utilisateurs Linux et macOS.

### 7. Questions probables d'un mainteneur

**« Un sous-processus de plus, ça coûte combien ? »**
Un seul appel `git check-ignore` par revue, quel que soit le nombre de fichiers modifiés, parce que les chemins passent groupés sur l'entrée standard. Il est court-circuité quand il n'y a aucun candidat. À côté des `git diff` et des lectures de fichiers que la revue fait déjà, c'est marginal.

**« Pourquoi `--no-index` ? Ça change le sens de la commande. »**
Sans `--no-index`, `check-ignore` répond « non ignoré » pour tout fichier présent dans l'index, donc pour tous les fichiers suivis. Or le projet appliquait déjà ses règles d'exclusion aux fichiers suivis avant cette PR, avec son filtre maison. `--no-index` préserve cette intention tout en déléguant le calcul à git.

**« Pourquoi garder un repli au lieu d'échouer si git ne répond pas ? »**
Pour ne pas transformer une situation dégradée en panne. Le filtre de repli est celui qui existait déjà ; il est moins juste, donc la PR l'accompagne d'un avertissement sur stderr. La seule exception est l'annulation du contexte, qui remonte en erreur : traiter une annulation comme « rien n'est ignoré » produirait une revue silencieusement fausse.

**« Le préfixe `./` devant un nom qui commence par `:`, c'est un bricolage ? »**
C'est la parade documentée. `git check-ignore` n'accepte pas `--literal-pathspecs`, donc un argument commençant par `:` serait lu comme de la magie de pathspec plutôt que comme un nom de fichier. Préfixer `./` force la lecture littérale, et le préfixe est retiré de la réponse. Le test `TestGitIgnoredPathsTakesNamesLiterally` couvre exactement ce cas.

**« Pourquoi ne pas utiliser une bibliothèque Go de `.gitignore` ? »**
Parce que le problème n'est pas d'avoir un meilleur moteur de motifs, mais d'avoir **le même verdict que git** : ordre de résolution, fichiers imbriqués, `info/exclude`, configuration globale, particularités par système. `check-ignore` est la seule source qui ne puisse pas diverger. Le dépôt dépend par ailleurs déjà de `doublestar` pour les motifs `**`, qui reste utilisé dans le filtre de repli.

**« Le changement de périmètre de revue ne va-t-il pas surprendre des utilisateurs ? »**
Si, potentiellement. Un dépôt avec des `.gitignore` imbriqués verra un ensemble de fichiers différent. C'est un rapprochement du comportement de git, pas un ajout de politique, mais cela mérite une ligne dans les notes de version. La PR le rend au moins prévisible : ce que voit la revue est désormais ce que voit `git check-ignore`.

**« Et `matchGitignoreBody`, pourquoi le corriger si on ne s'en sert plus ici ? »**
Parce qu'on s'en sert encore ailleurs. `internal/scan/provider.go` et `internal/tool/file_find.go` l'appellent à travers `diff.IsPathExcluded`. Le bug de séparateur les touchait aussi sous Windows ; le corriger là où il est règle le problème pour les trois consommateurs.

---

## PR #1472 — Analyser les en-têtes `diff --git` cités et ambigus

**Branche** : `fix/diff-header-quoted-paths` · **Fichiers touchés** : `internal/diff/diffheader.go` (nouveau), `internal/diff/parser.go`, plus les tests (`internal/diff/parser_test.go`, `internal/diff/git_test.go`).

### 1. Le problème en une phrase

L'en-tête `diff --git` était reconnu par l'expression régulière `^diff --git a/(.+?) b/(.+)$`, qui échoue sur deux formes réelles : les chemins que git met entre guillemets, et les chemins qui contiennent eux-mêmes la séquence ` b/`.

### 2. Pourquoi ça compte

Quand l'expression régulière ne reconnaît pas l'en-tête, le parseur ne démarre pas un nouveau fichier : il continue d'accumuler les lignes dans le fichier **précédent**. Le résultat est visible dans le test `TestParseDiffText_QuotedHeaderStartsNewFile` : le contenu du fichier cité, ici une fonction nommée `Secret()`, se retrouve collé dans le diff de `a.go`. Le relecteur (humain ou modèle) voit du code attribué au mauvais fichier, et le fichier cité n'est jamais relu du tout.

Le second cas est plus discret. Pour un fichier `x b/y.go` (un répertoire nommé `x b`), la ligne est `diff --git a/x b/y.go b/x b/y.go`. L'expression régulière, non gourmande, coupe à la première occurrence : elle rend `OldPath = "x"` et `NewPath = "y.go b/x b/y.go"`. Le fichier ne peut plus être ouvert, donc son contenu n'est pas lu et la revue porte sur un chemin qui n'existe pas.

### 3. Comment le bug se produit

**Ce que fait git avec les guillemets.** Git entoure un chemin de guillemets et l'échappe à la manière du langage C dès qu'il contient un `"`, un `\` ou un caractère de contrôle. Ce comportement n'est **pas** désactivé par `core.quotepath=false` : cette option ne concerne que les octets non-ASCII. La ligne devient alors, par exemple, `diff --git "a/b\"q.go" "b/b\"q.go"`. Chaque côté est cité indépendamment : un renommage peut donc mêler un jeton cité et un jeton nu.

1. `internal/diff/parser.go`, dans `ParseDiffText` : la boucle testait chaque ligne contre `diffHeaderRe`.
2. Sur un en-tête cité, la ligne commence par `diff --git "a/...` : le motif exige `diff --git a/`, il n'y a pas de correspondance.
3. Faute de correspondance, la variable `current` n'est pas remplacée. Les lignes suivantes — y compris les `@@` et les lignes ajoutées — sont écrites dans le tampon du fichier précédent.
4. Sur un en-tête non cité contenant ` b/` dans le chemin, il y a correspondance, mais au mauvais endroit : le `(.+?)` non gourmand s'arrête au premier ` b/` rencontré.

### 4. Ce que fait la correction

L'expression régulière est supprimée et remplacée par une fonction d'analyse dédiée, `parseDiffHeader` (`internal/diff/diffheader.go:25`), appelée depuis `parser.go:71`. Elle traite trois formes :

- **Premier jeton cité** : `cutQuoted` (`diffheader.go:120`) décode la chaîne citée en style C — les échappements `\a \b \t \n \v \f \r \" \\` et les séquences octales à trois chiffres, c'est-à-dire exactement ce que produit la fonction `quote_c_style` de git — et rend le reste de la ligne. Le second jeton est ensuite lu cité ou nu.
- **Premier jeton nu, second cité** : on cherche la première occurrence de ` "b/`. C'est sûr, parce qu'un chemin nu ne peut pas contenir de `"` : git l'aurait cité.
- **Deux jetons nus** : `splitBareHeader` (`diffheader.go:83`) privilégie la coupure où les deux moitiés sont **identiques**. Pour un fichier simplement modifié, l'en-tête est exactement `a/P b/P`, donc la bonne coupure est celle qui rend deux fois le même chemin — et elle est unique, même si `P` contient ` b/`. À défaut, on retombe sur la première occurrence de ` b/`, c'est-à-dire l'ancien comportement.

Dans tous les cas, les préfixes `a/` et `b/` doivent être présents et le chemin restant non vide, sinon la ligne est rejetée.

Par ailleurs, les lignes `rename from ` et `rename to ` passent désormais par `unquoteGitPath` (`diffheader.go:107`), qui décode un chemin cité et laisse un chemin nu inchangé. Ces deux lignes restent la source faisant autorité pour un renommage — elles l'étaient déjà avant la PR, avec un commentaire qui le disait — mais elles n'étaient pas décitées.

**Pourquoi ne pas simplement élargir l'expression régulière.** Le décodage de chaînes citées avec échappements octaux n'est pas un travail d'expression régulière : il faut décoder, pas seulement reconnaître. Et la désambiguïsation « les deux moitiés sont égales » suppose de comparer deux sous-chaînes, ce qu'une expression régulière POSIX ne fait pas.

Le commentaire de `parseDiffHeader` reconnaît explicitement ce qui reste non résolu : un **renommage** dont les deux chemins contiennent ` b/` et dont aucun côté n'est cité reste ambigu depuis l'en-tête seul. Ce cas est rattrapé par les lignes `rename from` / `rename to`.

### 5. La preuve

Quatre tests unitaires sur le parseur, deux tests d'intégration sur du vrai git.

- `parser_test.go`, `TestParseDiffText_QuotedHeaderStartsNewFile` : un diff de trois fichiers, dont deux avec un en-tête cité (l'un avec un `"` dans le nom, l'autre avec une tabulation et un `\`). Il exige trois diffs, vérifie que le second a le bon chemin (`b"q.go`), le drapeau `IsNew` et deux insertions, et — assertion la plus parlante — que le diff de `a.go` **ne contient pas** `Secret`. Sur `main`, ce test obtient un seul diff et l'assertion sur `Secret` affiche le diff fusionné.
- `parser_test.go`, `TestParseDiffText_QuotedRename` : deux renommages où un seul côté est cité, dans un sens puis dans l'autre, avec une séquence octale `\303\251` qui est l'encodage UTF-8 de `é`. Il vérifie les deux chemins et le drapeau `IsRenamed`.
- `parser_test.go`, `TestParseDiffText_PathContainingSpaceB` : le cas `x b/y.go`, sans guillemets.
- `parser_test.go`, `TestParseDiffHeader_Malformed` : quatorze lignes qui **ressemblent** à un en-tête doivent être rejetées — chemin vide, guillemet non fermé, guillemets accolés, texte en trop après le second jeton, échappement invalide (`\q`), séquence octale tronquée (`\1`), mauvais préfixe (`c/x`), et `diff --cc`. C'est le test qui empêche la nouvelle fonction d'être trop permissive et d'inventer des fichiers. Il vérifie aussi que `unquoteGitPath` décode tous les échappements et laisse une entrée non citable telle quelle.
- `git_test.go`, `TestCommitDiffSeparatesQuotedPath` : le même scénario avec un vrai dépôt et un vrai `git show`. Comme `"` n'est pas un caractère valide dans un nom de fichier Windows, le test n'écrit pas le fichier sur le disque : il crée le blob avec `git hash-object -w`, puis l'insère dans l'index avec `git -c core.protectNTFS=false update-index --add --cacheinfo`. Il vérifie les deux chemins, l'absence de contamination de `a.go`, et que le contenu du fichier cité a bien pu être lu à la révision (`NewFileContent` contient `func Secret`).
- `git_test.go`, `TestWorkspaceDiffPathContainingSpaceB` : un vrai répertoire nommé `x b`, un fichier modifié dedans, et la vérification que le chemin et le contenu sont corrects.

Pourquoi ces tests prouvent bien **ce** défaut : les deux tests d'intégration ne fabriquent pas de texte de diff, ils font produire l'en-tête par git lui-même. Le texte problématique n'est donc pas une hypothèse du contributeur.

### 6. Limites et effets de bord

- Un renommage dont **les deux** chemins contiennent ` b/` et dont aucun côté n'est cité reste ambigu au niveau de l'en-tête. Les lignes `rename from` / `rename to` corrigent les chemins juste après, mais l'en-tête seul ne suffit pas. C'est écrit dans le commentaire de la fonction.
- Les diffs combinés de fusion (`diff --cc`) ne sont toujours pas analysés. Ce n'est pas une régression : le projet demande `--diff-merges=first-parent`, donc git ne produit pas cette forme. Le test de rejet inclut d'ailleurs `diff --cc file.go` pour figer ce comportement.
- Les caractères `"` et les caractères de contrôle ne sont pas de noms de fichiers valides sous Windows. Le cas cité concerne donc surtout des dépôts créés ailleurs, ou une intégration continue Linux — ce qui reste fréquent.
- Le code ajouté (168 lignes dans un nouveau fichier) est plus long qu'une expression régulière. C'est le prix du décodage explicite ; il est isolé dans son propre fichier et entièrement couvert par les tests.
- Pour un en-tête que la nouvelle fonction rejette, le comportement est identique à celui d'aujourd'hui quand l'expression régulière ne correspondait pas : la ligne est traitée comme du contenu du fichier courant.

### 7. Questions probables d'un mainteneur

**« Pourquoi ne pas corriger l'expression régulière au lieu d'écrire un parseur ? »**
Parce qu'il ne s'agit pas seulement de reconnaître la ligne, mais de la **décoder** : les chemins cités contiennent des échappements, y compris octaux, qu'il faut convertir en octets. Et la désambiguïsation du cas ` b/` repose sur la comparaison des deux moitiés, qu'une expression régulière ne peut pas exprimer.

**« Est-ce que ça risque de casser le cas courant, qui marchait ? »**
Le cas courant passe par `splitBareHeader`, qui commence par tester la coupure « les deux moitiés sont égales » — exactement la forme `a/P b/P` d'un fichier modifié — et retombe sinon sur la première occurrence de ` b/`, c'est-à-dire ce que faisait l'ancienne expression régulière. Les tests existants du parseur couvrent le cas courant et continuent de passer.

**« Pourquoi ne pas demander à git un format machine, du genre `--raw` ou `-z` ? »**
Ce serait un changement bien plus large : tout le pipeline lit aujourd'hui le texte du diff unifié, parce qu'il a besoin des hunks eux-mêmes, pas seulement de la liste des fichiers. Cette PR corrige le parseur existant à son périmètre. Un passage à un format machine pour la seule liste des chemins est une piste distincte, à discuter séparément.

**« Un fichier nommé `b"q.go`, c'est réaliste ? »**
Le nom du test est volontairement extrême pour rendre le mécanisme visible, mais le déclencheur ne l'est pas : il suffit d'un `"`, d'un `\` ou d'un caractère de contrôle **n'importe où** dans le chemin pour que git cite la ligne. Un antislash dans un nom de fichier est banal sur les dépôts qui hébergent des fixtures de test ou des données. Et le second bug, le ` b/` dans le chemin, ne demande qu'un répertoire dont le nom se termine par une espace suivie d'un `b`.

**« Le décodage octal accepte-t-il n'importe quoi ? »**
Non. `cutQuoted` n'accepte que les échappements que git produit, et une séquence octale doit avoir exactement trois chiffres octaux valides, avec un premier chiffre entre `0` et `3` (un octet tient sur huit bits). Tout le reste fait échouer l'analyse, donc la ligne est rejetée plutôt que mal décodée. Le test `TestParseDiffHeader_Malformed` couvre `\q` et `\1`.

**« Quel est le coût en performance ? »**
Il devrait baisser plutôt que monter : l'ancienne version évaluait une expression régulière sur **chaque** ligne du diff, y compris les lignes de contenu. La nouvelle commence par un `strings.CutPrefix` sur `"diff --git "`, qui élimine immédiatement la quasi-totalité des lignes.

---

## PR #1483 — Passer les scripts de configuration MCP à `cmd.exe` sans les déformer

**Branche** : `fix/windows-shell-quoting` · **Fichiers touchés** : `cmd/opencodereview/shell_windows.go`, `cmd/opencodereview/shell_windows_test.go` (nouveau).

### 1. Le problème en une phrase

Sous Windows, un script de configuration de serveur MCP contenant des guillemets arrivait déformé à `cmd.exe`, donc il échouait et le serveur MCP était écarté de la revue.

### 2. Pourquoi ça compte

La forme la plus courante d'un script de configuration est justement celle qui casse : un exécutable dont le chemin contient une espace, donc entre guillemets, suivi d'arguments — `"C:\Program Files\tool\x.exe" --flag`.

Ce que voit l'utilisateur est décrit par le site d'appel, `cmd/opencodereview/review_cmd.go:566-584` : si la commande de configuration échoue, `ocr` affiche une erreur puis `Skipping MCP server %q — review will proceed without it.` La revue se poursuit donc **sans l'outil**, avec des résultats moins bons, et le message d'erreur ne dit pas que le problème vient du passage des guillemets.

### 3. Comment le bug se produit

**La notion sous-jacente.** Sous Unix, un processus reçoit un **tableau** d'arguments. Sous Windows, il reçoit une **seule chaîne** : la ligne de commande. C'est le programme appelé qui la redécoupe. Go, dans `os/exec`, construit cette chaîne à partir de `Cmd.Args` en appliquant `syscall.EscapeArg`, qui suit la convention de la fonction Windows `CommandLineToArgvW` : un `"` interne devient `\"`.

Or `cmd.exe` est une exception documentée — la documentation de `exec.Command` le signale — : il n'applique pas cette convention et ne comprend pas les séquences `\"`.

1. `shell_windows.go` faisait `exec.CommandContext(ctx, "cmd", "/c", script)`.
2. Pour `script = "C:\Program Files\tool\x.exe" --flag`, `EscapeArg` produit une ligne de commande où les guillemets du script sont devenus `\"`.
3. `cmd.exe` lit ces `\"` comme des caractères ordinaires. Il n'a plus de chemin entre guillemets, seulement une suite de mots séparés par des espaces, et il cherche un exécutable nommé `"C:\Program`.
4. La commande échoue, `CombinedOutput` rend une erreur, et le serveur MCP est écarté.

### 4. Ce que fait la correction

`shellCommand` construit la ligne de commande à la main et la transmet **telle quelle** :

```
c := exec.CommandContext(ctx, "cmd.exe")
c.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /S /C "` + script + `"`}
```

Points de détail, tous justifiés par les commentaires du code :

- `SysProcAttr.CmdLine` court-circuite la construction depuis `Args` : `syscall.StartProcess` utilise `CmdLine` verbatim dès qu'elle est non vide et ne regarde pas `argv`. Le script n'est donc jamais échappé.
- `/S` demande à `cmd.exe` de retirer **exactement** la paire de guillemets extérieure qu'on ajoute et d'exécuter le reste sans y toucher.
- `Args` garde sa valeur par défaut à un élément plutôt que d'être vidée, parce que `Cmd.String()` indexe `Args[1:]` et paniquerait sur une tranche nulle.
- L'exécutable est écrit `cmd.exe`, avec son extension, pour qu'un fichier nommé `cmd` présent dans le `PATH` ne puisse pas se substituer au shell.
- Le commentaire avertit que tout appelant qui touchera plus tard à `SysProcAttr` doit **modifier** la structure existante et non la remplacer, sous peine de perdre `CmdLine`.

**Ne pas échapper le script est délibéré.** Le script vient du fichier de configuration MCP : c'est une ligne de commande que son auteur a demandé qu'on exécute, et le pendant Unix passe la même chaîne à `sh -c`. Échapper les guillemets internes reviendrait à annuler la seule raison d'être de `CmdLine`.

**Pourquoi cette approche plutôt qu'une autre.** Elle n'est pas inventée pour cette PR : `internal/llm/keycmd_windows.go` fait déjà exactement cela, avec le même `/S /C`, le même `cmd.exe`, le même `Args` à un élément, pour la commande de récupération de clé d'API (`api_key_cmd`). Cette approche est donc déjà en place et déjà testée dans le dépôt ; la PR aligne le chemin « configuration MCP » sur elle. Le commentaire de `shellCommand` renvoie d'ailleurs à `keycmd_windows.go` pour l'argumentaire complet, afin de ne pas le dupliquer.

### 5. La preuve

Deux tests, tous deux sous `//go:build windows`, donc joués uniquement sur une machine ou un runner Windows.

- `TestShellCommand_CmdLine` fige la chaîne exacte produite : `cmd.exe /S /C ""C:\Program Files\tool\x.exe" --flag"`. Il vérifie d'abord que `SysProcAttr` n'est pas `nil`, avec un message explicite — « the command line would be built from Args instead » — parce que c'est précisément le symptôme sur `main`, où `SysProcAttr` est `nil`. Il vérifie aussi que `Args` n'est pas vide et que `Cmd.String()` ne panique pas.
- `TestShellCommand_QuotedScripts` **exécute réellement** trois scripts par `cmd.exe` et compare la sortie :
  - `echo plain` → `plain` (le cas nominal, pour montrer qu'on ne casse rien) ;
  - `echo "a b"` → `"a b"` (les guillemets survivent au passage) ;
  - un vrai fichier `setup.cmd` écrit dans un répertoire nommé `with space`, invoqué entre guillemets avec un argument → `setup ran hello`. C'est exactement la forme qui échoue sur `main`.

Pourquoi ces tests prouvent bien **ce** défaut : le premier est un test de structure, il ne peut échouer que si la ligne de commande n'est plus transmise verbatim. Le troisième cas du second test est un bout à bout : si l'échappement de Go intervenait, `cmd.exe` ne trouverait pas le fichier et le test échouerait avec la sortie brute de `cmd.exe` affichée.

### 6. Limites et effets de bord

- **Windows uniquement.** `shell_unix.go` n'est pas touché.
- **Les scripts ne sont pas portables entre les deux branches.** `%VAR%` et `^` sont des métacaractères de `cmd.exe`, alors que `$VAR` et l'échappement par `\` appartiennent à `sh`. Un script de configuration écrit pour Unix demande généralement une réécriture pour Windows. C'est déjà le cas aujourd'hui ; la PR ne change rien sur ce point, mais ne le résout pas non plus.
- **`/S` a ses propres règles, et elles ne couvrent pas toutes les formes.** Les tests de `internal/llm/keycmd_windows_test.go` documentent lesquelles : un guillemet isolé à l'intérieur du script laisse la suite dans une zone citée, tandis qu'un guillemet **doublé** referme cette zone, si bien qu'un `&` qui suit redevient un séparateur de commandes. Pour un script de configuration MCP ce n'est pas une frontière de sécurité — l'auteur de la configuration peut déjà exécuter ce qu'il veut — mais c'est une forme où la ligne se scinde.
- **Pas d'échappement = pas de barrière d'injection ajoutée.** Il n'y en avait pas non plus avant : le script est une commande fournie par la configuration, exactement comme sur le pendant Unix avec `sh -c`.
- **Interaction avec la PR #1430** : celle-ci modifie `configureProcessGroup` pour Windows, qui est appelée sur le même `*exec.Cmd` juste après `shellCommand`. Elle définit `cmd.Cancel` et `cmd.WaitDelay`, et ne touche pas à `SysProcAttr` — donc `CmdLine` est préservé. C'est exactement l'avertissement que porte le commentaire de `shellCommand`.

### 7. Questions probables d'un mainteneur

**« Ne pas échapper le script, ce n'est pas une faille d'injection ? »**
Non, parce qu'il n'y a rien à protéger à ce niveau. Le script vient du fichier de configuration MCP : son auteur demande explicitement qu'on exécute cette ligne de commande, et la branche Unix passe déjà la même chaîne à `sh -c`. Échapper les guillemets internes casserait précisément le cas pour lequel on écrit ce code, sans empêcher quoi que ce soit.

**« Pourquoi `/S` et pas simplement `/C` ? »**
`/S` donne à `cmd.exe` une règle de désenrobage simple et prévisible : il retire exactement la paire de guillemets extérieure qu'on ajoute et exécute le reste sans y toucher. Sans `/S`, `cmd.exe` applique une heuristique plus compliquée sur le nombre de guillemets présents, dont le résultat dépend du contenu du script.

**« Pourquoi ne pas découper le script en arguments et passer un tableau ? »**
Parce qu'il faudrait pour cela réimplémenter les règles de découpage de `cmd.exe`, qui ne sont pas celles de `CommandLineToArgvW`. Et surtout le champ de configuration est documenté comme une **ligne de commande de shell**, pas comme une liste d'arguments : elle peut contenir `&&`, des redirections, des variables. La découper changerait le contrat.

**« Est-ce que les scripts simples continuent de marcher ? »**
Oui, et c'est testé : le premier cas de `TestShellCommand_QuotedScripts` est `echo plain`. La paire de guillemets ajoutée autour du script est retirée par `/S`, donc un script sans guillemet est transmis à l'identique.

**« Pourquoi dupliquer la logique de `keycmd_windows.go` au lieu de la partager ? »**
Les deux fonctions vivent dans des paquets différents (`main` et `internal/llm`) et n'ont pas les mêmes appelants ni les mêmes délais. La PR choisit de dupliquer trois lignes et de renvoyer au commentaire existant pour l'argumentaire, plutôt que d'introduire un paquet partagé pour cela. Si vous préférez une fonction commune, c'est un changement mécanique — dites-le et on l'extrait.

**« Comment être sûr que `configureProcessGroup` ne va pas écraser `SysProcAttr` ? »**
Sur Windows, `configureProcessGroup` ne définit que `cmd.Cancel` et `cmd.WaitDelay` ; elle ne touche pas à `SysProcAttr`. Le commentaire de `shellCommand` pose la règle pour l'avenir : un futur appelant doit modifier la structure existante, pas la remplacer. C'est la seule contrainte que cette PR ajoute au reste du code.

---

## PR #1430 — Tuer tout l'arbre de processus quand un script de configuration MCP dépasse son délai

**Branche** : `fix/windows-setup-process-tree` · **Fichiers touchés** : `cmd/opencodereview/procattr_windows.go`, `cmd/opencodereview/procattr_windows_test.go` (nouveau).

### 1. Le problème en une phrase

Sous Windows, quand un script de configuration MCP dépassait son délai de cinq minutes, seul `cmd.exe` était tué ; le programme qu'il avait lancé survivait, et comme il tenait encore le tuyau de sortie, `ocr` restait bloqué indéfiniment.

### 2. Pourquoi ça compte

Le site d'appel (`cmd/opencodereview/review_cmd.go:568-575`) accorde cinq minutes à la commande de configuration, puis lit sa sortie avec `CombinedOutput()`. Sur Windows, ce budget n'était pas appliqué : un `npm install` bloqué ne rendait jamais la main, donc `ocr` ne rendait jamais la main non plus. La revue ne démarrait pas, sans message. Et des processus orphelins restaient derrière.

Le code sur `main` le disait explicitement, en commentaire dans `procattr_windows.go` :

> On Windows, exec.CommandContext sends os.Kill which terminates the direct child. Grandchild processes (e.g. from sh -c) may survive. Full process-tree cleanup would need Windows Job Objects, but sh -c is rare on Windows so this is an acceptable limitation.

La prémisse « `sh -c` is rare on Windows » est le point faible : le chemin en question n'est pas `sh -c`, c'est `cmd /c`, qui est utilisé **systématiquement** pour tout script de configuration MCP sous Windows. Le petit-fils n'est pas un cas rare, c'est le cas normal.

### 3. Comment le bug se produit

**Les notions sous-jacentes.** `cmd /c <script>` lance `cmd.exe`, qui lance à son tour le vrai programme. Ce dernier est donc un **petit-fils** du processus `ocr`. Sous Unix, `procattr_unix.go` résout cela avec `Setpgid` (mettre tout le monde dans un même groupe de processus) puis `syscall.Kill(-pid, SIGKILL)`, qui touche le groupe entier. Windows n'a pas cet équivalent.

Par ailleurs, quand Go lance un processus avec `CombinedOutput()`, il lui donne un tuyau pour stdout et stderr. Les descendants **héritent** de ce tuyau. `cmd.Wait()` ne rend la main qu'une fois le tuyau fermé par **tous** ceux qui le détiennent.

1. Le délai de cinq minutes expire, donc le contexte est annulé.
2. La fonction `Cancel` par défaut d'`exec.CommandContext` tue `cmd.Process`, c'est-à-dire `cmd.exe` seulement.
3. Le petit-fils (`npm`, `node`, ...) survit et continue de détenir le tuyau hérité.
4. `cmd.Wait()`, appelé depuis `CombinedOutput()`, attend la fermeture du tuyau. Il attend donc que le petit-fils se termine de lui-même. Le délai est annulé en pratique.

C'est le second point qui fait la gravité du bug : ce n'est pas seulement « un processus reste en vie », c'est « l'outil se bloque ».

### 4. Ce que fait la correction

`configureProcessGroup` (version Windows) définit désormais deux choses :

- **`cmd.Cancel`** lance `taskkill /T /F /PID <pid>`. `/T` signifie « terminer l'arborescence » — le processus et tous ses descendants — et `/F` force la terminaison. L'appel est lui-même borné par un contexte de dix secondes (`taskkillTimeout`), parce que `WaitDelay` ne peut pas interrompre une fonction `Cancel` encore bloquée à l'intérieur. Si `taskkill` échoue, on retombe sur `cmd.Process.Kill()` — c'est-à-dire exactement le comportement précédent, jamais pire.
- **`cmd.WaitDelay = 5 * time.Second`** (`setupWaitDelay`) : si malgré tout un descendant a échappé à `taskkill` et tient encore le tuyau, `Wait` abandonne au bout de cinq secondes au lieu de bloquer sans fin. C'est la ceinture en plus des bretelles, et c'est ce qui garantit que le délai de cinq minutes redevient effectif quoi qu'il arrive.

Les deux durées sont des constantes nommées avec un commentaire qui dit ce qu'elles bornent.

**Pourquoi cette approche plutôt qu'une autre.** Le commentaire remplacé désignait l'alternative : les *Job Objects* de Windows, un mécanisme du noyau qui permet de regrouper des processus et de les terminer ensemble. C'est la solution la plus robuste, mais elle demande de manipuler des handles Windows et des appels système supplémentaires, et de gérer leur cycle de vie. `taskkill` est un outil livré avec Windows, invocable en une ligne, et couvre le cas visé. La PR garde en outre l'ancien `Kill()` comme repli, donc l'échec de `taskkill` ne dégrade rien.

### 5. La preuve

Un test, `procattr_windows_test.go`, `TestConfigureProcessGroup_CancelKillsGrandchild`, sous `//go:build windows`.

Son déroulement :

1. Il lance, via `shellCommand`, une commande PowerShell qui écrit **son propre PID** dans un fichier temporaire puis dort 120 secondes. Le test connaît donc l'identifiant du petit-fils.
2. Il branche `Stdout` et `Stderr` sur **un même tampon**, comme le fait `CombinedOutput()`. C'est essentiel : cela reproduit la condition de blocage, pas seulement la survie du processus.
3. Il attend que le fichier de PID apparaisse (délai de 30 secondes), puis vérifie avec `tasklist` que le petit-fils tourne bien **avant** l'annulation. Sans cette vérification, un test qui passerait parce que le processus n'a jamais démarré serait indiscernable d'un test qui passe pour la bonne raison.
4. Il annule le contexte, puis exige que `cmd.Wait()` rende la main en moins de 30 secondes. Sur `main`, c'est ici que le test échoue, avec le message « Wait did not return after cancellation » : le tuyau est toujours détenu par le petit-fils qui dort 120 secondes.
5. Enfin il exige que le PID disparaisse de `tasklist` en moins de 10 secondes. C'est l'assertion qui distingue « `Wait` est revenu grâce à `WaitDelay` » de « l'arbre a réellement été tué ».
6. Un `t.Cleanup` tue le petit-fils dans tous les cas, pour qu'un test en échec ne laisse pas traîner un processus de 120 secondes.

Deux détails montrent que le test est isolé du reste :

- La commande PowerShell est transmise en `-EncodedCommand`, c'est-à-dire en base64 d'UTF-16LE. Il n'y a donc **aucun guillemet** à faire traverser `cmd /c`, et le test ne dépend pas de la PR #1483.
- `processRunning` interroge `tasklist` avec un filtre sur le PID et un format CSV, et cherche le PID entre guillemets, pour ne pas confondre avec une correspondance partielle.

### 6. Limites et effets de bord

- **`taskkill` doit être présent dans le `PATH`.** Il est livré avec Windows, mais s'il manque, on retombe sur `cmd.Process.Kill()`, c'est-à-dire l'ancien comportement : pas de régression, pas d'amélioration.
- **`/F` est une terminaison brutale.** Le script de configuration n'a aucune chance de nettoyer derrière lui. C'est le même choix que sur la branche Unix, qui envoie `SIGKILL`.
- **Le périmètre est très réduit** : `configureProcessGroup` n'a qu'un seul appelant en production, la commande de configuration MCP dans `review_cmd.go:572`. Les serveurs MCP eux-mêmes, lancés ensuite, ne passent pas par là.
- **La branche Unix n'est pas touchée.**
- **Le test est lent** — jusqu'à une minute et demie dans le pire cas — et exige PowerShell, `tasklist` et `taskkill`. Il ne s'exécute que sur un runner Windows.
- **Cinq secondes de `WaitDelay` est une valeur choisie, pas mesurée.** Elle borne uniquement le temps de drainage après que l'arbre a été tué.

### 7. Questions probables d'un mainteneur

**« Pourquoi `taskkill` et pas des Job Objects ? »**
Les Job Objects sont plus propres et plus robustes, mais ils demandent de créer et de gérer des handles Windows et d'ajouter des appels système au projet. `taskkill /T` est livré avec le système et tient en une ligne, et le repli sur `cmd.Process.Kill()` fait qu'un échec ne dégrade rien par rapport à aujourd'hui. Si l'équipe préfère les Job Objects, la surface à remplacer est cette seule fonction.

**« Un `taskkill` sur un PID, n'y a-t-il pas un risque de réutilisation d'identifiant ? »**
Pour le processus direct, non : Go garde un handle ouvert sur le processus tant que `Wait` n'a pas été appelé, et Windows ne recycle pas un PID tant qu'un handle reste ouvert. C'est `taskkill` lui-même qui parcourt l'arborescence pour trouver les descendants, à partir de ce PID stable.

**« Pourquoi une limite de temps sur `taskkill` lui-même ? »**
Parce que `WaitDelay` ne s'applique pas à la fonction `Cancel`. Si `taskkill` restait bloqué, `Cancel` ne rendrait jamais la main et le mécanisme d'annulation resterait coincé à l'intérieur de son propre remède. Les dix secondes garantissent que `Cancel` rend toujours la main.

**« `WaitDelay` ne suffirait-il pas, sans `taskkill` ? »**
Non. `WaitDelay` débloquerait `ocr`, mais laisserait le petit-fils tourner — un `npm install` qui continue en arrière-plan après la fin de la revue. Les deux mécanismes répondent à deux problèmes : `taskkill` arrête le travail, `WaitDelay` garantit qu'on ne reste pas bloqué si l'arrêt a été incomplet.

**« Le commentaire supprimé disait que le cas était rare. Pourquoi le contredire ? »**
Parce qu'il parlait de `sh -c`, qui est effectivement rare sous Windows. Mais le chemin réel est `cmd /c`, utilisé pour **tous** les scripts de configuration MCP sous Windows. Le petit-fils n'est donc pas un cas limite, c'est la structure normale de chaque exécution.

**« Le test est long et dépend de PowerShell — est-ce acceptable ? »**
Il est marqué `//go:build windows`, donc il ne pèse que sur le travail Windows de l'intégration continue. Ses délais sont généreux pour rester stable sur une machine chargée, mais le chemin nominal est rapide : le test se termine dès que `Wait` revient et que le PID a disparu. Si le temps de cycle pose problème, on peut le placer derrière `testing.Short()`.

**« Pourquoi le test partage-t-il un seul tampon entre `Stdout` et `Stderr` ? »**
Pour reproduire ce que fait `CombinedOutput()` au site d'appel réel. C'est ce partage qui fait que le petit-fils hérite du tuyau de sortie, et c'est cet héritage qui bloquait `Wait`. Avec deux tampons distincts, le test montrerait seulement qu'un processus survit, pas que l'outil se fige.
