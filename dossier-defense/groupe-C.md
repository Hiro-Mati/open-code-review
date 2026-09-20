# Dossier de défense — Groupe C

> **Note de vérification.** Les messages d'échec cités dans ce dossier ont été reconstitués à partir du code des tests. Les résultats réellement observés en exécutant les tests sans leur correction sont consignés dans `preuves-execution.md` : citez ceux-là devant un mainteneur.


Projet : `alibaba/open-code-review` (Go).
Cinq pull requests : #1475, #1478, #1482, #1433, #1435.

Ce document sert à défendre chaque changement devant les mainteneurs. Chaque
section suit le même plan : le problème, pourquoi il compte, le mécanisme, la
correction, la preuve, les limites, puis les questions probables.

---

## Petit lexique commun

Ces notions reviennent dans plusieurs PR. Elles sont expliquées ici une fois
pour ne pas alourdir les sections.

**Session enregistrée sur disque.** Quand l'outil relit du code, il écrit un
journal dans `~/.opencodereview/sessions/<dossier encodé>/<id de session>.jsonl`.
Le format JSONL veut dire : un objet JSON par ligne, ajouté au fil de l'eau. On
y trouve une ligne `session_start` (qui mémorise entre autres le répertoire de
travail, `cwd`), une ligne par fichier relu (`review_item_done`,
`review_item_reused`, `review_item_failed`) et une ligne `session_end`.

**Reprise (resume).** `ocr review --resume <id>` relit le journal d'une session
précédente et réutilise les résultats des fichiers déjà traités au lieu de les
renvoyer au modèle. C'est l'intérêt : on ne paye pas deux fois les mêmes appels.

**Empreinte (fingerprint).** Pour décider si un fichier a déjà été traité, l'outil
calcule un condensé SHA-256 du mode de relecture, du chemin et du contenu du
fichier (voir `scanItemFingerprint`, `internal/scan/agent.go:292`). Même
empreinte = même fichier, même contenu, donc le résultat précédent est
réutilisable.

**Scan de fichiers entiers.** `ocr scan` ne regarde pas un diff : il envoie des
fichiers complets au modèle. Le code vit dans `internal/scan/`, la relecture de
diff dans `internal/agent/`. Les deux ont une structure très proche, ce qui
explique que plusieurs de ces PR touchent les deux en parallèle.

**SARIF.** *Static Analysis Results Interchange Format*, un format JSON
normalisé (OASIS, version 2.1.0) pour publier les résultats d'un outil
d'analyse statique. Son intérêt : GitHub Code Scanning, les IDE et d'autres
outils savent le lire sans connaître l'outil qui l'a produit. Un résultat SARIF
dit où se trouve le problème via un objet `artifactLocation`, dont le champ
`uri` doit être une référence URI valide.

**OpenTelemetry.** Une bibliothèque standard pour produire des traces et des
métriques. Ici elle est encapsulée dans `internal/telemetry/`. Les métriques
sont désactivées par défaut et ne s'activent que si l'utilisateur configure un
exportateur (`telemetry.IsEnabled()`, `internal/telemetry/provider.go:73`).

**Histogramme de métrique.** Un compteur additionne des nombres. Un histogramme,
lui, enregistre des *échantillons* individuels et les range dans des tranches
(« buckets »). Il garde aussi le nombre total d'échantillons (`Count`) et leur
somme (`Sum`). C'est ce qui permet de calculer une moyenne ou une médiane :
`ocr.review.duration_seconds` est un histogramme, chaque exécution devrait y
déposer exactement un échantillon.

---

## PR #1475 — Ne plus renvoyer les sessions d'un autre dépôt

**Branche** : `fix/session-repo-isolation` · **Fichiers touchés** :
`internal/session/list.go`, `internal/session/list_test.go`,
`internal/session/persist.go`, `internal/session/persist_test.go`,
`internal/session/resume.go`, `internal/viewer/store_test.go`

### 1. Le problème en une phrase

Deux dépôts dont les chemins sont différents peuvent partager le même dossier de
sessions sur le disque, et rien ne vérifiait à la lecture à quel dépôt une
session appartenait.

### 2. Pourquoi ça compte

Trois conséquences concrètes.

D'abord, `ocr session list` peut afficher les sessions d'un autre projet. C'est
une fuite d'information : les identifiants de session, les noms de branches et
les chemins de fichiers d'un projet apparaissent dans un autre.

Ensuite, et c'est plus grave, `ocr review --resume <id>` peut accepter une
session qui appartient à un autre dépôt. Les remarques enregistrées pour les
fichiers de l'autre projet sont alors réutilisées telles quelles et publiées
comme si elles venaient de ce dépôt-ci. Le résultat est faux, et rien ne le
signale.

Enfin, un dépôt situé sur un partage réseau Windows (chemin dit UNC, de la forme
`\\serveur\partage\projet`) écrivait ses sessions à un endroit que la visionneuse
HTML n'explorait jamais. Ses sessions existaient mais restaient invisibles.

### 3. Comment le bug se produit

**Étape 1 — l'encodage du chemin perd de l'information.**
`encodeRepoPath` (`internal/session/persist.go:76`) transforme le chemin du
dépôt en un nom de dossier : il retire le nom de volume, coupe les séparateurs
de tête, puis remplace chaque `/` et chaque `\` par un tiret `-`.

Le tiret n'est pas un caractère réservé : il peut déjà figurer dans un nom de
dossier. Donc deux chemins distincts peuvent produire le même nom :

- `/home/moi/a-b/c`  → `home-moi-a-b-c`
- `/home/moi/a/b-c`  → `home-moi-a-b-c`

C'est une collision. L'encodage n'est pas injectif.

**Étape 2 — l'écriture.** `jsonlWriter.open()` (`internal/session/persist.go:143`)
crée `~/.opencodereview/sessions/<encodeRepoPath(repoDir)>/` et y écrit le
fichier `.jsonl`. Les deux dépôts de l'étape 1 écrivent donc dans le *même*
dossier.

**Étape 3 — la lecture ne filtrait rien.** `ListSessions(repoDir)`
(`internal/session/list.go:110`) recalcule ce même nom de dossier, liste tous
les fichiers `.jsonl` qu'il contient et retourne un résumé pour chacun. Avant le
correctif, aucun test ne comparait le dépôt réellement enregistré dans le
fichier au dépôt demandé.

**Étape 4 — la reprise non plus.** `loadResumeState`
(`internal/session/resume.go:111`) ouvre le fichier par son identifiant de
session dans ce dossier partagé. Un identifiant qui existe suffisait. Or un
identifiant de session est un UUID : il est unique, mais le trouver dans le
dossier ne prouve pas qu'il vous appartient, puisque le dossier est partagé.

**L'information manquante existait pourtant.** La ligne `session_start` du
journal contient le champ `cwd`, c'est-à-dire le répertoire de travail réel au
moment de l'exécution. `applyRecordToSummary` (`internal/session/list.go:241`)
le recopie déjà dans `Summary.RepoDir`, et `applySessionStart` fait de même pour
l'état de reprise. Personne ne s'en servait pour filtrer.

**Le cas UNC, en plus.** Sur Windows, `filepath.VolumeName("\\\\serveur\\partage\\projet")`
renvoie `\\serveur\partage`. Ce volume n'était pas transformé : les deux
antislashs de tête étaient conservés tels quels dans le nom de dossier. Quand
`filepath.Join` assemblait le chemin final, il nettoyait ces séparateurs et le
résultat se retrouvait *imbriqué* : `sessions/serveur/partagerepo` au lieu de
`sessions/<un seul segment>`. Or `DiscoverRepos` (`internal/viewer/store.go:45`)
ne descend que d'un niveau sous la racine et exige des fichiers `.jsonl`
directement dans ce niveau. Il ne voyait donc jamais ces sessions.

### 4. Ce que fait la correction

Elle ajoute deux choses distinctes.

**A. Une vérification d'appartenance à la lecture.**

Une fonction `sameRepoDir(recorded, repoDir)` (`internal/session/list.go:190`)
compare le dépôt enregistré dans le journal au dépôt demandé. Elle est appelée à
deux endroits :

- dans `ListSessions` (`internal/session/list.go:136`) : une session qui ne
  correspond pas est simplement ignorée, sans erreur ;
- dans `loadResumeState` (`internal/session/resume.go:149`) : une session qui ne
  correspond pas provoque une erreur explicite, qui nomme les deux dépôts.

Les deux traitements sont volontairement différents. Lister est une opération de
consultation : une session étrangère n'a rien à y faire, on la saute en silence.
Reprendre est une opération qui produit un résultat publié : mieux vaut échouer
bruyamment que produire un rapport contaminé.

`sameRepoDir` normalise avec `filepath.Clean` avant de comparer. Cela absorbe un
séparateur final et, sur Windows, convertit les `/` en `\`. C'est nécessaire
parce que `git rev-parse --show-toplevel` renvoie un chemin avec des barres
obliques même sous Windows, et que l'outil s'appuie dessus
(`cmd/opencodereview/shared.go:185`). Sur Windows et macOS la comparaison est
aussi insensible à la casse, comme le sont leurs systèmes de fichiers par défaut.

Un détail important : si une session n'a enregistré aucun `cwd`,
`loadSummaryFromFile` (`internal/session/list.go:198`) initialise `RepoDir` avec
le `repoDir` demandé. Elle correspond donc toujours. C'est délibéré : sans
information, on ne peut pas prouver qu'elle est étrangère, et la faire
disparaître serait une régression pour les journaux anciens ou tronqués.

**B. Un encodage correct pour les volumes UNC, et seulement pour eux.**

`isUNCVolume` (`internal/session/persist.go:113`) reconnaît un volume qui
commence par deux séparateurs : `\\serveur\partage`, mais aussi les préfixes
d'appareil `\\?\` et `\\.\`. `encodeUNCVolume` (`internal/session/persist.go:127`)
l'aplatit en un seul segment préfixé `UNC-`, en remplaçant les séparateurs par
des tirets et les caractères interdits dans un nom de fichier Windows
(`: ? * " < > |`) par des soulignés.

**Pourquoi ne pas changer l'encodage des dossiers existants — le point à
défendre.**

La tentation naturelle, face à une collision, est de remplacer tout l'encodage
par quelque chose d'injectif : un condensé du chemin, un encodage réversible du
type percent-encoding, etc. Ce n'est pas ce que fait cette PR, et c'est un choix
assumé.

Raison 1 : le nom du dossier est une donnée persistante, pas un détail interne.
Chaque utilisateur a déjà, sur son disque, des dossiers nommés selon l'ancien
encodage. Changer la règle rend tous ces dossiers introuvables d'un coup :
`ocr session list` renverrait une liste vide, `--resume` échouerait, et la
visionneuse n'afficherait plus l'historique. On corrigerait une fuite rare en
cassant une fonctionnalité utilisée tous les jours.

Raison 2 : un nouvel encodage ne corrigerait pas vraiment le fond. Le nom du
dossier ne peut pas être la source de vérité sur l'appartenance d'une session,
parce qu'il est calculé, alors que le journal, lui, *contient* la réponse. La
vérification du `cwd` est la bonne réponse, et elle rend la question de
l'injectivité de l'encodage secondaire : deux dépôts peuvent continuer à
partager un dossier sans que personne ne voie les sessions de l'autre.

Raison 3 : le cas UNC est différent, et c'est pourquoi il est traité. Là,
l'ancien encodage ne produisait pas une collision mais un chemin *mal formé*,
imbriqué à un niveau que le code ne va jamais chercher. Ces sessions n'étaient
déjà pas exploitables par la visionneuse. Le coût du changement est donc limité
à ce seul cas, qui est le seul à emprunter la nouvelle branche de code : les
lettres de lecteur (`C:\...`) et les chemins POSIX (`/home/...`) passent par
exactement le même code qu'avant, et leurs dossiers existants restent valides.
Le commentaire au-dessus de `encodeUNCVolume` dit cela explicitement.

### 5. La preuve

Quatre tests, plus un pour la visionneuse.

`TestListSessions_SkipsOtherRepoInSharedDirectory`
(`internal/session/list_test.go`). L'assistant `collidingRepoDirs` construit
`base/a-b/c` et `base/a/b-c`, et vérifie d'abord, par `t.Fatalf`, que les deux
encodent bien vers le même nom — si un jour l'encodage change, le test dit
clairement que son hypothèse ne tient plus, plutôt que de réussir pour de
mauvaises raisons. Il écrit ensuite une vraie session pour chacun, via la couche
de persistance réelle, et vérifie que `ListSessions` de chaque côté ne renvoie
que la sienne. Sur `main`, le test échoue avec un message du type
« `ListSessions(...) = [deux résumés]`, want only own session ... ».

`TestLoadResumeState_RejectsOtherRepoInSharedDirectory`. Il écrit une session
pour le dépôt B, puis tente `LoadResumeState` et `LoadReviewResumeState` depuis
le dépôt A. Les deux doivent échouer. Un troisième appel, depuis le dépôt
propriétaire, doit réussir et retrouver son unique élément terminé — c'est ce
qui garantit qu'on a bien ajouté un filtre et pas cassé la reprise. Sur `main`,
le test échoue en disant « `LoadResumeState(A)` loaded a session recorded in B ».

`TestSameRepoDir`. Un test unitaire de la fonction de comparaison : chemins
identiques, séparateur final, écriture avec barres obliques (le cas
`--show-toplevel` sous Windows), et deux dépôts vraiment différents.

`TestEncodeRepoPath` (`internal/session/persist_test.go`) reçoit trois cas
Windows supplémentaires : `\\server\share\repo\sub` → `UNC-server-share-repo-sub`,
`\\server\share\` → `UNC-server-share`, et `\\?\C:\code\myapp` →
`UNC-_-C_-code-myapp`. Ce sont des attentes exactes, caractère par caractère.

`TestDiscoverRepos_FindsUNCRepoSessions` (`internal/viewer/store_test.go`) est le
test de bout en bout du volet UNC : il crée une vraie session pour
`\\server\share\repo`, calcule la racine des sessions à partir d'un dépôt à
lettre de lecteur, et vérifie que `DiscoverRepos` trouve exactement un dépôt avec
une session. Sur `main`, il trouverait zéro dépôt. Il est ignoré
(`t.Skip`) hors Windows, puisque `filepath.VolumeName` n'y renvoie jamais de
volume UNC.

### 6. Limites et effets de bord

**Les sessions déjà enregistrées pour un chemin réseau UNC ne seront plus
retrouvées.** C'est l'effet de bord le plus important et il faut l'annoncer
soi-même. Avant cette PR, écriture et lecture utilisaient le même ancien
encodage : `ocr session list` retrouvait donc bien ces sessions, même si la
visionneuse ne les voyait pas. Après cette PR, le nom de dossier calculé change,
donc l'ancien dossier devient orphelin. Les fichiers ne sont pas supprimés, ils
ne sont simplement plus consultés par l'outil. Le contournement manuel est de
renommer le dossier ; aucune migration automatique n'est proposée.

**La visionneuse HTML n'est pas filtrée.** `internal/viewer/store.go` a son
propre `ListSessions(root, encodedRepo)` (ligne 115) qui travaille à partir du
nom de dossier encodé, pas d'un chemin de dépôt. Dans un dossier partagé par
collision, la visionneuse continue donc d'afficher les sessions des deux dépôts.
Le correctif couvre les commandes `ocr session ...` et la reprise, pas la
visionneuse. C'est une limite réelle, pas un oubli caché : la visionneuse n'a pas
de chemin de dépôt à comparer.

**La sensibilité à la casse est décidée par le système d'exploitation, pas par
le système de fichiers.** `sameRepoDir` utilise `runtime.GOOS`. Sur Linux avec
un volume monté insensible à la casse, deux écritures du même dépôt ne
correspondraient pas. Sur macOS avec un volume APFS sensible à la casse, deux
dépôts distincts ne différant que par la casse seraient confondus. Ce sont des
cas rares, et l'alternative (interroger le système de fichiers) serait coûteuse
et faillible.

**Une session sans `cwd` correspond toujours.** Voir plus haut : c'est
intentionnel, mais cela veut dire que le filtre ne protège pas contre un journal
dont la première ligne est absente ou corrompue.

**Nouvelle collision théorique.** Un dépôt POSIX situé à `/UNC-server-share`
encoderait vers `UNC-server-share`, comme `\\server\share` sous Windows. Les
deux ne coexistent pas sur la même machine dans la pratique, et de toute façon la
vérification du `cwd` les sépare désormais.

### 7. Questions probables d'un mainteneur

**« Pourquoi ne pas simplement rendre `encodeRepoPath` injectif, par exemple en
ajoutant un condensé du chemin ? »**
Parce que le nom du dossier est une donnée déjà écrite sur les disques des
utilisateurs. Tout changement de règle rend leurs sessions existantes
introuvables. Et surtout, cela ne corrigerait pas la bonne chose : le journal
contient déjà le répertoire réel, c'est lui la source de vérité. Le vérifier
suffit, et coûte une comparaison de chaînes.

**« Alors pourquoi changer l'encodage pour UNC, si le principe est de ne pas y
toucher ? »**
Parce que ce n'était pas une collision mais un chemin cassé. Les antislashs du
volume faisaient imbriquer le dossier de session plus bas que la racine, à un
endroit que `DiscoverRepos` n'explore pas. Ces sessions étaient donc déjà
partiellement inutilisables. Seuls les volumes UNC empruntent la nouvelle
branche : les lettres de lecteur et les chemins POSIX passent par le même code
qu'avant, octet pour octet.

**« Un utilisateur qui avait des sessions sur un partage réseau va les
perdre ? »**
Il ne les perd pas, les fichiers restent sur le disque, mais l'outil ne les
retrouvera plus car il calcule un nouveau nom de dossier. On peut renommer le
dossier à la main. Je n'ai pas ajouté de migration automatique : elle
supposerait de deviner l'ancien nom à partir du nouveau, ce qui n'est pas
toujours possible, et elle réécrirait des fichiers de l'utilisateur sans qu'il
l'ait demandé. Si vous préférez une migration ou un simple avertissement, je
peux l'ajouter.

**« Pourquoi ignorer en silence dans `ListSessions` et lever une erreur dans
`loadResumeState` ? »**
Les deux opérations n'ont pas le même risque. Lister sert à consulter : une
entrée étrangère est du bruit, on l'enlève. Reprendre produit un rapport publié :
si la session ne vient pas de ce dépôt, ses remarques seraient attribuées à des
fichiers qui ne sont pas les siens. Un échec avec un message qui nomme les deux
dépôts vaut mieux qu'un rapport silencieusement faux.

**« Le `filepath.Clean` et la comparaison insensible à la casse ne risquent-ils
pas d'assouplir trop le filtre ? »**
`Clean` ne fait que normaliser une écriture du même chemin : séparateur final,
`.` intermédiaires, et sous Windows les barres obliques que renvoie
`git rev-parse --show-toplevel`. Sans cela, le filtre rejetterait des sessions
légitimes. La comparaison insensible à la casse suit le comportement par défaut
de Windows et de macOS, où `C:\Repo` et `c:\repo` désignent le même dossier.

**« Que se passe-t-il pour une session dont la ligne `session_start` n'a pas de
`cwd` ? »**
Elle correspond toujours, donc elle reste visible. `loadSummaryFromFile`
initialise `RepoDir` avec le dépôt demandé, et l'état de reprise fait de même.
Sans information, on ne peut rien prouver ; la faire disparaître serait une
régression pour les journaux anciens. C'est écrit dans le commentaire au-dessus
de `sameRepoDir`.

---

## PR #1478 — Conserver les résultats réutilisés quand tous les fichiers neufs échouent

**Branche** : `fix/scan-resume-keeps-reused` · **Fichiers touchés** :
`internal/scan/agent.go`, `internal/scan/coverage_test.go`

### 1. Le problème en une phrase

Lors d'une reprise de `ocr scan`, si tous les fichiers restant à traiter
échouent, la commande signale un échec total et jette les résultats déjà obtenus
lors de la session précédente.

### 2. Pourquoi ça compte

La reprise existe précisément pour ne pas refaire un travail déjà payé.
L'utilisateur reprend un scan parce que le précédent s'est interrompu, par
exemple après une coupure réseau ou une limite d'API. Si le fournisseur du
modèle est toujours indisponible, les quelques fichiers restants échouent — et
avec le comportement actuel, la commande sort en erreur.

Or, quand `ocr scan` sort en erreur, `runScan` (`cmd/opencodereview/scan_cmd.go:246-252`)
retourne immédiatement et n'appelle jamais `emitRunResult`. Rien n'est donc
publié : ni JSON, ni SARIF, ni texte. Les résultats réutilisés, qui étaient
pourtant valides et déjà sur le disque, disparaissent de la sortie. L'utilisateur
se retrouve avec moins que ce qu'il avait avant de relancer la commande.

Il y a aussi une incohérence de comportement entre les deux commandes : le
chemin de relecture de diff, `internal/agent/agent.go:843`, traite déjà ce cas
correctement. `ocr review` conserve ses résultats réutilisés, `ocr scan` non.
Deux commandes voisines se comportent différemment sur la même situation.

### 3. Comment le bug se produit

**Étape 1 — la comptabilité de la reprise.** Au début de `dispatchSubtasks`
(`internal/scan/agent.go:553`), `initResumeInfo` (ligne 261) parcourt tous les
fichiers à traiter et les classe en deux catégories, selon que leur empreinte
figure ou non dans l'état de reprise :

- `ReusedFiles` : le fichier était déjà traité, on réutilise son résultat ;
- `RerunFiles` : le fichier doit être envoyé au modèle.

**Étape 2 — la dépêche.** La boucle sur les lots appelle `dispatchBatch`, qui
renvoie `n`, le nombre de sous-tâches réellement *dépêchées* vers le modèle.
Elles s'accumulent dans `dispatched`. Les fichiers réutilisés ne sont pas
dépêchés : ils ne comptent pas dans `dispatched`.

**Étape 3 — le compteur d'échecs.** Chaque sous-tâche qui échoue incrémente
`a.subtaskFailed` de façon atomique (`internal/scan/agent.go:761` et `:772`).
Le mot « atomique » veut simplement dire que plusieurs goroutines peuvent
incrémenter le compteur en parallèle sans se marcher dessus.

**Étape 4 — le test fautif.** À la fin de `dispatchSubtasks`, juste après la
boucle sur les lots, le code comparait :

```go
if failed > 0 && failed == dispatched {
    return nil, fmt.Errorf("all %d file scan(s) failed — ...")
}
```

La condition se lit « tout ce qui a été dépêché a échoué ». Elle est juste pour
une exécution neuve : dans ce cas, `dispatched` est bien le total du travail.
Mais dans une reprise, `dispatched` n'est que la partie *neuve*. Si un seul
fichier neuf restait et qu'il échoue, on a `failed == dispatched == 1`, donc
erreur — alors que les fichiers réutilisés sont déjà dans le collecteur de
remarques et n'ont, eux, pas échoué.

**Étape 5 — la perte.** La fonction renvoie `nil` comme liste de remarques. Le
collecteur contenait pourtant les remarques réutilisées, mais elles ne sont pas
retournées, et l'appelant abandonne sur l'erreur.

### 4. Ce que fait la correction

Une seule condition supplémentaire. On lit le nombre de fichiers réutilisés dans
`a.resumeInfo` (nul quand il n'y a pas de reprise) et on n'émet l'erreur « tout a
échoué » que s'il vaut zéro :

```go
reused := int64(0)
if a.resumeInfo != nil {
    reused = a.resumeInfo.ReusedFiles
}
if failed > 0 && failed == dispatched && reused == 0 {
    return nil, fmt.Errorf("all %d file scan(s) failed — ...")
}
```

**Pourquoi cette approche.** Trois arguments.

D'abord, elle préserve exactement le comportement existant pour une exécution
neuve. Sans reprise, `a.resumeInfo` est nul, `reused` vaut 0, la condition est
identique à celle d'avant. Aucun scénario connu ne change de comportement.

Ensuite, elle réutilise une valeur déjà calculée. `ReusedFiles` est renseigné
plus haut dans la même fonction par `initResumeInfo`, et sert déjà à afficher la
ligne « Resume ... : reusing N file(s) ». On ne rajoute pas d'état, on ne
recompte rien.

Enfin, elle aligne le scan sur la relecture de diff. `internal/agent/agent.go:838-849`
fait déjà exactement ce test, avec le même raisonnement. Le commentaire du
correctif le dit (« As in review ») pour que le lien soit visible au relecteur.
Un mainteneur qui a déjà accepté ce raisonnement d'un côté n'a pas à le
réexaminer de l'autre.

Le sens du changement est de reclasser la situation : ce n'est pas une exécution
ratée, c'est une exécution *partielle*. Une exécution partielle doit publier ce
qu'elle a. Les fichiers neufs qui ont échoué ne sont pas passés sous silence
pour autant : chacun produit un avertissement, visible via `a.Warnings()`
(`internal/scan/agent.go:223`) et publié dans la sortie.

### 5. La preuve

Un test : `TestDispatchSubtasks_ResumeKeepsReusedWhenAllFreshFail`
(`internal/scan/coverage_test.go`).

Il construit la situation minimale : deux fichiers, `cached.go` et `fresh.go`.
`cached.go` est présent dans l'état de reprise, avec une remarque enregistrée
(« cached finding ») et l'empreinte que `scanItemFingerprint` calcule pour lui —
le test appelle la vraie fonction d'empreinte, il ne fabrique pas une valeur à la
main. `fresh.go` n'y est pas. Le client LLM est un faux client qui échoue
systématiquement (`errorScanClient` avec `context.DeadlineExceeded`).

Il vérifie ensuite trois choses :

1. `dispatchSubtasks` ne renvoie pas d'erreur. Sur `main`, le test échoue ici,
   avec le message « a resumed scan with a reused file must not fail when every
   fresh file fails: all 1 file scan(s) failed — check your LLM configuration
   and API key ». Ce message est utile en soi : il montre que l'erreur produite
   parle de « 1 fichier » alors que le scan en comptait deux.
2. Les remarques retournées contiennent exactement la remarque réutilisée. C'est
   ce qui prouve que le résultat n'est pas seulement « pas une erreur », mais
   qu'il porte bien le contenu qu'on voulait sauver.
3. `a.Warnings()` contient exactement un avertissement, pour `fresh.go`. C'est ce
   qui prouve que l'échec n'est pas escamoté.

Le test précédent, `TestDispatchSubtasks_AllFailed`, reste inchangé et continue
de vérifier qu'une exécution neuve dont tout échoue produit bien une erreur.
C'est le garde-fou contre une correction trop large.

### 6. Limites et effets de bord

**Le code de sortie change dans ce cas précis.** Avant, `ocr scan --resume` avec
tous les fichiers neufs en échec sortait en erreur (code non nul). Maintenant il
sort en succès, avec un résultat partiel et des avertissements. Une intégration
continue qui s'appuyait sur ce code de sortie pour détecter une panne du
fournisseur de modèle ne la détectera plus ainsi. Il faut regarder les
avertissements, ou l'état terminal du manifeste. C'est le comportement que
`ocr review` a déjà, donc ce n'est pas une nouveauté dans le projet, mais c'est
une nouveauté pour `ocr scan`.

**Un seul fichier réutilisé suffit.** La condition est `reused == 0`, pas un
seuil. Si la session précédente n'avait traité qu'un fichier sur mille, et que
les 999 restants échouent, la commande réussit avec une couverture de 0,1 %.
C'est cohérent avec la relecture de diff, et la couverture reste lisible dans le
manifeste et les avertissements, mais il faut le savoir.

**Le correctif ne touche pas au seuil de couverture.** Il ne change rien à la
façon dont le manifeste calcule un état `partial` ou `failed`. Il ne fait que
cesser de jeter les données.

**Portée limitée au scan.** Un cas voisin existe peut-être ailleurs (par exemple
une annulation de contexte en cours de lot), mais il n'est pas traité ici.

### 7. Questions probables d'un mainteneur

**« Pourquoi ne pas plutôt compter les fichiers réutilisés dans `dispatched` ? »**
Parce que `dispatched` a un sens précis : le nombre de sous-tâches envoyées au
modèle. Il sert aussi à formuler le message d'erreur (« all N file scan(s)
failed »). Y ajouter des fichiers qui n'ont jamais été dépêchés rendrait le
message faux et changerait la signification d'une variable utilisée ailleurs.
Ajouter une condition explicite est plus lisible et plus sûr.

**« Est-ce que l'utilisateur voit que des fichiers ont échoué, ou est-ce qu'on
cache le problème ? »**
Il le voit. Chaque fichier en échec produit un avertissement, et le test vérifie
qu'il y en a exactement un pour `fresh.go`. Ces avertissements sont publiés dans
la sortie JSON et dans le rapport texte. Ce qui change, c'est qu'on ne jette plus
les résultats valides en même temps.

**« Et si l'utilisateur relance encore, avec la même panne ? Il tourne en
rond ? »**
Les fichiers réutilisés restent réutilisés, les fichiers neufs échouent à
nouveau, et la sortie reste la même : un résultat partiel plus des
avertissements. L'utilisateur ne progresse pas tant que le fournisseur est en
panne, mais il ne régresse pas non plus, ce qui est le point.

**« Pourquoi cette différence existait-elle entre `review` et `scan` ? »**
Les deux pipelines ont une structure jumelle mais des fichiers séparés. Le
correctif a été appliqué côté relecture de diff (`internal/agent/agent.go`) et
n'a pas été reporté côté scan. Cette PR fait ce report, en gardant la même
formulation pour que les deux restent comparables à la lecture.

**« Le test dépend-il de détails internes qui vont bouger ? »**
Il appelle `dispatchSubtasks` directement et fabrique `a.items`, donc oui, il est
lié à la structure interne de l'agent. C'est cohérent avec les tests voisins du
même fichier, qui procèdent ainsi. L'avantage est qu'il n'a pas besoin d'un vrai
dépôt git ni d'une vraie session complète, donc il reste rapide et lisible.

**« Le comportement de `--resume` avec zéro fichier réutilisé est-il encore
testé ? »**
Oui, par `TestDispatchSubtasks_AllFailed`, qui n'a pas été modifié : sans
reprise, tous les fichiers échoués donnent toujours une erreur. Le nouveau test
ne relâche la condition que lorsqu'un fichier est réellement réutilisé.

---

## PR #1482 — Corriger trois défauts d'encodage dans les sorties

**Branche** : `fix/output-encoding` · **Fichiers touchés** :
`cmd/opencodereview/output.go`, `cmd/opencodereview/output_manifest_test.go`,
`cmd/opencodereview/sarif.go`, `cmd/opencodereview/sarif_test.go`,
`internal/viewer/server.go`, `internal/viewer/server_test.go`

### 1. Le problème en une phrase

Trois sorties de l'outil produisent des données mal formées : le champ
`comments` du JSON vaut `null` au lieu de `[]` après une exécution en échec, le
champ `uri` du SARIF n'est pas une référence URI valide, et la troncature de
texte de la visionneuse peut couper au milieu d'un caractère.

### 2. Pourquoi ça compte

Ce sont trois défauts du même genre : la sortie est *syntaxiquement* acceptée
mais *sémantiquement* fausse, donc le consommateur se trompe sans être prévenu.

Pour le JSON : un script qui fait `for c in result["comments"]` plante sur
`null` en Python, et en JavaScript `result.comments.length` lève une erreur. Le
cas est fréquent, puisqu'il survient précisément quand une exécution a échoué —
c'est-à-dire quand le script d'intégration a le plus besoin de lire proprement
la sortie.

Pour le SARIF : GitHub Code Scanning, et tout autre consommateur, résout le champ
`uri` comme une référence URI. Un chemin comme `docs/notes#2/a.go` sera compris
comme le fichier `docs/notes` avec un fragment `2/a.go`. Le problème est alors
rattaché au mauvais fichier, ou ignoré. Pire, un chemin contenant déjà un `%`
sera *décodé* : `100%.md` peut devenir un autre nom, ou une séquence invalide.

Pour la troncature : la visionneuse coupe les chemins de fichiers longs. Un
chemin non-ASCII (accent français, idéogramme) est codé sur plusieurs octets en
UTF-8 ; couper à un décalage d'octet quelconque produit une séquence invalide.
La page HTML servie n'est alors plus de l'UTF-8 valide. La fonctionnalité
d'export d'une session en fichier HTML autonome fige ce défaut dans un fichier
archivé.

### 3. Comment le bug se produit

**Défaut 1 — `"comments": null`.**

La structure `jsonOutput` (`cmd/opencodereview/output.go:319`) déclare
`Comments []model.LlmComment` avec l'étiquette `json:"comments"`, sans
`omitempty`. En Go, une tranche (slice) nulle est sérialisée en `null`, alors
qu'une tranche vide non nulle est sérialisée en `[]`. La distinction est
invisible dans le code Go mais visible dans la sortie.

Quand une exécution échoue, `Agent.Run` renvoie `nil` comme liste de remarques.
Or `review_cmd.go:295` décide de publier quand même :

```go
emitted := manifest != nil || runErr == nil
```

Autrement dit, dès qu'un manifeste a pu être construit, le résultat est publié
même si l'exécution a échoué — c'est voulu, pour que les consommateurs JSON
gardent le diagnostic de couverture. Mais on passe alors `nil` à
`outputJSONWithWarnings` (`cmd/opencodereview/output.go:354`), et le JSON sort
avec `"comments": null`.

Les deux autres écrivains du projet ne font pas cela : `outputJSONNoFiles`
(ligne 636) initialise explicitement `Comments: []model.LlmComment{}`, et
l'écrivain SARIF fait `make([]sarifResult, 0, len(comments))` (ligne 197), ce qui
donne toujours un tableau. L'incohérence est donc interne au projet, pas
seulement vis-à-vis du monde extérieur.

**Défaut 2 — l'URI SARIF.**

`sarifResultFromComment` (`cmd/opencodereview/sarif.go:244`) plaçait le chemin du
fichier tel quel dans `artifactLocation.uri`, à deux endroits : la localisation
du résultat et la localisation du correctif proposé.

La spécification SARIF 2.1.0 (section 3.4.3) exige une référence URI. Un chemin
brut n'en est pas une. Quatre caractères posent problème :

- `#` démarre un fragment ; tout ce qui suit sort du chemin ;
- `?` démarre une requête ; idem ;
- `%` est le préfixe d'une séquence encodée, donc `%41` serait décodé en `A` ;
- `:` dans le *premier* segment d'une référence relative se lit comme le
  séparateur d'un schéma, donc `c:/x.go` est interprété comme le schéma `c`.

**Défaut 3 — la troncature.**

`truncateText` (`internal/viewer/server.go:483`) est exposée aux gabarits HTML
sous le nom `truncate` (ligne 331). Les gabarits l'utilisent, par exemple
`{{sessionTaskLabel .FilePath | truncate 60}}` dans `session.html`.

Le code d'origine faisait `return s[:n] + "…"`. En Go, `len(s)` sur une chaîne
compte des *octets*, pas des caractères. UTF-8 encode un caractère sur un à
quatre octets : `é` en occupe deux, `界` en occupe trois. Couper à l'octet `n`
peut donc tomber au milieu d'une séquence, et produire un octet de continuation
orphelin. Ce n'est plus de l'UTF-8 valide.

Le test existant passait parce que ses cas tombaient par chance sur des
frontières : `truncateText(6, "你好世界")` coupe après deux caractères de trois
octets. Avec `n = 7`, il coupait au milieu du troisième.

### 4. Ce que fait la correction

**Défaut 1.** Trois lignes dans `outputJSONWithWarnings` (`output.go:364`) :
si `comments` est nul, on le remplace par une tranche vide. La normalisation est
faite au point de sortie, pas chez les appelants, parce qu'il y en a plusieurs et
qu'il suffit qu'un seul oublie. Le commentaire justifie le choix en nommant les
deux autres écrivains que l'on aligne.

On aurait pu ajouter `omitempty` à l'étiquette, mais cela ferait *disparaître* le
champ, ce qui casserait tout autant les consommateurs. Un tableau vide est la
bonne réponse : « la question a été posée, la réponse est zéro remarque ».

**Défaut 2.** Une fonction `sarifArtifactURI` (`cmd/opencodereview/sarif.go:281`)
découpe le chemin sur `/`, encode chaque segment avec `url.PathEscape`, puis
recolle avec `/`.

Le découpage par segment est le point important. `url.PathEscape` sur le chemin
entier encoderait aussi les `/`, ce qui donnerait `a%2Fb%2Fc.go` : une URI
valide, mais qui ne désigne plus un chemin à trois segments. En encodant segment
par segment, les `/` séparateurs survivent et le consommateur retrouve le chemin
d'origine.

Un cas reste à traiter à la main : `url.PathEscape` laisse le `:` tel quel,
parce qu'il est légal *à l'intérieur* d'un segment de chemin. Mais dans le
premier segment d'une référence relative, il serait lu comme un séparateur de
schéma. La fonction le remplace donc par `%3A` partout. Le faire partout plutôt
que dans le premier segment seulement est volontaire : `%3A` se décode en `:` de
toute façon, donc le chemin reconstitué est identique, et la règle est plus
simple à lire et à tester.

**Défaut 3.** Une boucle de deux lignes avant la coupe :

```go
for n > 0 && !utf8.RuneStart(s[n]) {
    n--
}
```

`utf8.RuneStart(b)` répond « cet octet est-il le premier octet d'un caractère ? ».
Tant que la réponse est non, on recule. On coupe donc toujours sur une frontière
de caractère, en enlevant au plus trois octets. C'est un recul, jamais une
avance : le résultat ne dépasse jamais `n` octets, donc la garantie de longueur
que les gabarits attendent est préservée. Si le recul va jusqu'à zéro (la
troncature tombe à l'intérieur du tout premier caractère), il ne reste que
l'ellipsis.

### 5. La preuve

**`TestOutputJSONWithWarnings_FailedRunCommentsNotNull`**
(`cmd/opencodereview/output_manifest_test.go`). Il reproduit exactement la
situation décrite : un manifeste dont l'état terminal est `StateFailed`, et
`nil` pour les remarques. Il désérialise la sortie en
`map[string]json.RawMessage`, ce qui permet de regarder le texte JSON brut du
champ plutôt qu'une valeur Go déjà normalisée. Sur `main`, le test échoue avec
« comments = null, want [] ».

**`TestSarifArtifactURI_EncodesPathSegments`** (`cmd/opencodereview/sarif_test.go`).
Un test tabulaire avec quatre chemins : un chemin ordinaire qui doit rester
inchangé, `docs/C# notes/100%.md`, `a?b/c;d,e.go`, et `c:/x.go`. Pour chacun, il
vérifie l'URI produite aux *deux* emplacements (le résultat et le correctif), ce
qui garantit qu'on n'a pas corrigé l'un en oubliant l'autre.

La deuxième partie du test est la plus convaincante : il repasse l'URI dans
`url.Parse` et vérifie que le schéma, le fragment et la requête sont vides, et
que `u.Path` est *exactement* le chemin d'origine. C'est une preuve d'aller-retour,
pas seulement une comparaison de chaînes : elle dit que le consommateur retrouve
bien le fichier voulu.

Sur `main`, pour `docs/C# notes/100%.md`, l'échec affiche
« uri = "docs/C# notes/100%.md", want "docs/C%23%20notes/100%25.md" », puis,
pour `c:/x.go`, « uri "c:/x.go" resolves to scheme="c" path="/x.go" ... » — ce
message montre directement que le consommateur ne voit pas le bon chemin.

**`TestTruncateText`** (`internal/viewer/server_test.go`) reçoit trois cas
supplémentaires : une coupe à l'intérieur d'un caractère (`n = 7` sur `你好世界`),
une coupe à l'intérieur du tout premier caractère (`n = 2` sur `你好`), et un
chemin réaliste avec un préfixe ASCII puis un caractère accentué (`src/é.go`).
Surtout, une assertion `utf8.ValidString(got)` est ajoutée et s'applique à *tous*
les cas du tableau, anciens compris. Sur `main`, le cas `n = 7` échoue deux fois :
une fois sur la valeur attendue, une fois sur la validité UTF-8.

Les commentaires `// allow-non-english` sur ces lignes sont exigés par la règle
« anglais uniquement » du projet ; ils étaient déjà présents sur les cas
existants, la PR les reconduit.

### 6. Limites et effets de bord

**La sortie SARIF change pour les chemins contenant des caractères réservés.**
C'est le but, mais c'est un changement observable. Un consommateur qui comparait
la chaîne brute à un chemin de dépôt verra une différence. Un consommateur
conforme (qui résout l'URI) ne verra rien changer. Les chemins ordinaires
ASCII sans caractère réservé sont inchangés octet pour octet, ce que le premier
cas du test vérifie explicitement.

**L'espace devient `%20`.** C'est correct au sens de la spécification et cela
reste lisible, mais un humain qui lit le SARIF verra une différence sur les
chemins contenant des espaces.

**Le `:` est encodé dans tous les segments, pas seulement le premier.** C'est
plus large que le strict nécessaire. Le chemin reconstitué est identique, mais
un consommateur qui compare des chaînes brutes verra `%3A` là où il n'était pas
indispensable.

**La normalisation `nil` → `[]` ne couvre qu'une fonction.** Si une autre sortie
JSON était ajoutée un jour, il faudrait y penser. Une solution plus radicale
serait d'encapsuler la sérialisation, ce qui dépasse le cadre de cette PR.

**La troncature reste exprimée en octets.** `truncate 60` veut toujours dire
« au plus 60 octets », pas « 60 caractères ». Un chemin en idéogrammes sera donc
tronqué à une vingtaine de caractères visibles seulement. Ce comportement n'est
pas changé, uniquement rendu sûr. Un passage à un décompte en caractères serait
un changement d'interface pour les gabarits, à discuter séparément.

**Trois corrections dans une seule PR.** Elles partagent un thème (l'encodage
des sorties) mais touchent trois fichiers sans lien fonctionnel. Un mainteneur
peut demander de les séparer ; c'est un découpage facile à faire, chaque
correction étant indépendante des deux autres.

### 7. Questions probables d'un mainteneur

**« Pourquoi trois corrections dans une seule PR ? »**
Elles relèvent du même thème : une sortie destinée à être lue par un programme,
qui n'est pas conforme au format annoncé. Les trois sont petites et testées
séparément. Cela dit, elles sont totalement indépendantes, donc si vous préférez
trois PR, je peux découper sans rien réécrire.

**« Pourquoi encoder segment par segment plutôt que le chemin entier ? »**
Parce que `url.PathEscape` appliqué au chemin entier encoderait aussi les `/`
en `%2F`. On obtiendrait une URI valide, mais qui ne désigne plus un chemin à
plusieurs segments : le consommateur chercherait un fichier dont le nom contient
des barres obliques. En encodant chaque segment, les séparateurs survivent et
l'aller-retour redonne exactement le chemin d'origine — c'est précisément ce que
le test vérifie avec `url.Parse`.

**« Pourquoi remplacer le `:` à la main, et pourquoi partout ? »**
`url.PathEscape` le laisse tel quel parce qu'il est légal dans un segment de
chemin. Mais dans le premier segment d'une référence relative, `c:/x.go` se lit
comme le schéma `c`. Le remplacer partout plutôt que dans le seul premier segment
est un choix de simplicité : `%3A` se décode en `:`, donc le chemin reconstitué
est le même, et la règle n'a pas de cas particulier à tester.

**« `omitempty` sur le champ `comments` n'aurait-il pas suffi ? »**
Non, cela ferait disparaître le champ entièrement, ce qui casse autant les
consommateurs que `null`. Et cela le ferait disparaître aussi pour une exécution
réussie sans remarque, ce qui est un cas normal. Un tableau vide est la réponse
correcte : la clé existe, elle est vide.

**« Est-ce vraiment un bug qu'on puisse atteindre, ce `null` ? »**
Oui, et par un chemin explicite. `review_cmd.go:295` publie le résultat dès
qu'un manifeste a été construit, même quand l'exécution a échoué — c'est un
choix assumé du projet pour que le diagnostic de couverture parte quand même.
`Agent.Run` renvoie alors `nil`, donc `"comments": null`. Le test reproduit
exactement cet état.

**« Pourquoi reculer sur la frontière de caractère plutôt qu'avancer ? »**
Parce que reculer garantit que le résultat ne dépasse jamais la limite demandée.
Les gabarits utilisent `truncate` pour tenir dans une colonne ; avancer
dépasserait de un à trois octets. Reculer enlève au plus trois octets, et dans le
cas extrême où la coupe tombe dans le premier caractère, il ne reste que
l'ellipsis — ce cas est testé.

---

## PR #1433 — Ne compter la durée et les remarques qu'une fois par exécution

**Branche** : `fix/telemetry-double-count` · **Fichiers touchés** :
`cmd/opencodereview/shared.go`, `internal/agent/agent.go`,
`internal/scan/agent.go`, `internal/telemetry/testing.go` (nouveau),
`internal/scan/metrics_test.go` (nouveau),
`cmd/opencodereview/emit_run_result_test.go`

### 1. Le problème en une phrase

Deux métriques d'exécution, la durée de relecture et le nombre de remarques,
étaient enregistrées à deux endroits différents pour une même exécution, ce qui
les comptait deux fois.

### 2. Pourquoi ça compte

Les métriques servent à mesurer le coût et la performance de l'outil. Si elles
sont fausses, elles orientent mal les décisions.

Pour l'histogramme `ocr.review.duration_seconds`, deux échantillons par
exécution signifient que le nombre d'exécutions apparaît doublé, et que la
moyenne est faussée — les deux échantillons ne mesurent pas la même chose (voir
plus bas), donc ce n'est même pas un simple facteur deux.

Pour le compteur `ocr.comments_generated_total`, la somme est exactement
doublée. Un tableau de bord qui affiche « remarques par exécution » ou
« remarques par mille lignes » donne un chiffre deux fois trop grand.

Plus subtil : le double comptage n'était pas uniforme. `ocr review` publie son
résultat même quand l'exécution échoue, donc une exécution ratée passait par
`emitRunResult` et comptait deux fois. `ocr scan` sort en erreur sans publier,
donc une exécution ratée ne comptait qu'une fois. Les deux commandes n'étaient
donc pas comparables entre elles, ce qui est pire qu'une erreur systématique.

### 3. Comment le bug se produit

**Les points d'enregistrement.** `internal/telemetry/metrics.go` expose trois
fonctions : `RecordReviewDuration` (ligne 77), qui dépose un échantillon dans
l'histogramme, `RecordFilesReviewed` (ligne 87) et `RecordCommentsGenerated`
(ligne 97), qui incrémentent des compteurs. Chacune vérifie d'abord
`IsEnabled()` : si la télémétrie n'est pas configurée, elle ne fait rien.

**Avant le correctif, pour la durée.**

1. `Agent.dispatchSubtasks` (`internal/agent/agent.go`, et son jumeau
   `internal/scan/agent.go`) démarrait un chronomètre et appelait
   `RecordReviewDuration` dans un `defer`. Un `defer` en Go exécute la fonction
   au moment où l'on quitte la fonction englobante, quelle que soit la sortie.
   Cela mesurait la durée de la *dépêche des sous-tâches*.
2. `emitRunResult` (`cmd/opencodereview/shared.go`), appelé par les deux
   commandes après l'exécution, calculait `time.Since(startTime)` où `startTime`
   est pris juste avant `ag.Run(...)`, et appelait à nouveau
   `RecordReviewDuration`. Cela mesurait la durée de *toute l'exécution*.

Deux échantillons, deux grandeurs différentes, mélangés dans le même
histogramme.

**Avant le correctif, pour les remarques.**

1. `Agent.Run` appelait déjà `RecordCommentsGenerated` juste après
   `dispatchSubtasks`.
2. `emitRunResult` appelait à nouveau `RecordCommentsGenerated` avec la même
   liste, après résolution des numéros de ligne.

Le compteur était donc exactement doublé sur toute exécution qui atteignait
`emitRunResult`.

**Pourquoi c'est passé inaperçu.** La télémétrie est désactivée par défaut, donc
aucun test ni aucun usage courant ne voyait le problème. Et il n'existait aucun
moyen, depuis un autre paquet, de brancher un lecteur de métriques de test : les
variables d'état de `internal/telemetry` sont privées au paquet.

### 4. Ce que fait la correction

**Un seul propriétaire par métrique : l'agent.**

Les appels dans `emitRunResult` (`cmd/opencodereview/shared.go:829-833`) sont
supprimés. La couche commande n'enregistre plus rien. Le calcul de `duration`
reste, parce qu'il sert à remplir le résumé JSON, mais il n'alimente plus la
métrique. La documentation de la fonction est mise à jour en conséquence, et un
commentaire explique pourquoi.

**Le chronomètre remonte de `dispatchSubtasks` à `Run`.**

Dans les deux agents (`internal/agent/agent.go:287-288` et
`internal/scan/agent.go:320-321`), le `defer` est déplacé tout en haut de `Run` :

```go
runCtx, runStart := ctx, time.Now()
defer func() { telemetry.RecordReviewDuration(runCtx, time.Since(runStart)) }()
```

Deux détails valent d'être expliqués.

Le contexte est capturé dans une variable `runCtx` séparée. C'est nécessaire
parce que `ctx` est réaffecté plusieurs fois plus bas dans `Run`, chaque
`telemetry.StartSpan` renvoyant un contexte enrichi. Sans cette capture, la
fermeture (`closure`) du `defer` lirait la dernière valeur de `ctx`, qui
appartient à une étape déjà terminée. En capturant, on enregistre la métrique
dans le contexte de l'exécution entière, ce qui est le bon rattachement.

Le `defer` est placé avant tout traitement. Il s'exécute donc sur *tous* les
chemins de retour : succès, résultat partiel, échec dès l'analyse du diff. C'est
ce qui rend la mesure comparable d'une exécution à l'autre.

**Pourquoi cette approche plutôt qu'une autre.** L'alternative aurait été de
garder l'enregistrement dans la commande et de le retirer de l'agent. Elle a été
écartée parce que la commande ne voit pas toutes les exécutions : `ocr scan`
retourne avant `emitRunResult` quand l'exécution échoue
(`cmd/opencodereview/scan_cmd.go:246-252`). Une exécution échouée est
précisément celle dont on veut mesurer la durée. L'agent, lui, est traversé par
toutes les exécutions. C'est le bon propriétaire.

**Un utilitaire de test dans `internal/telemetry`.**

`EnableMetricsForTest(mp)` (`internal/telemetry/testing.go`) force l'état interne
du paquet à « activé », installe le fournisseur de métriques passé en paramètre,
réinitialise le drapeau `initMetricsOnce` pour que les instruments soient
recréés contre ce fournisseur, et renvoie une fonction qui restaure l'état
précédent. Sans cela, un test d'un autre paquet ne peut rien observer, puisque
`initialized`, `shutdownFuncs` et `initMetricsOnce` sont privés.

### 5. La preuve — ce qui est démontré et ce qui ne l'est pas

Il faut être honnête ici, parce qu'un mainteneur le verra.

**Ce qui est démontré.**

`TestRun_RecordsOneDurationSamplePerRun` (`internal/scan/metrics_test.go`) est
un test tabulaire sur trois scénarios : succès complet, échec partiel (le
premier appel au modèle échoue, les suivants réussissent), et échec total (le
faux client échoue plus d'un million de fois, donc toujours). Dans les trois
cas, il collecte les métriques par un `ManualReader` — un lecteur qui n'exporte
rien mais permet de demander un instantané — et compte les échantillons de
l'histogramme `ocr.review.duration_seconds`. L'attente est : exactement 1.

Le troisième cas vérifie aussi que `Run` renvoie bien une erreur : le test
prouve donc qu'on enregistre la métrique *même* quand l'exécution échoue, pas
qu'on l'a supprimée.

Sur `main`, ce test échoue avec « recorded 2 duration samples, want exactly 1 »
pour les cas où la dépêche est atteinte.

`TestEmitRunResult_RecordsNoRunMetrics` (`cmd/opencodereview/emit_run_result_test.go`)
prend le problème par l'autre bout : il appelle `emitRunResult` avec un faux
fournisseur de résultat et une remarque, puis vérifie qu'*aucune* métrique nommée
`ocr.review.duration_seconds` ou `ocr.comments_generated_total` n'apparaît dans
l'instantané. Sur `main`, il échoue en nommant la métrique fautive :
« emitRunResult recorded ocr.review.duration_seconds; run metrics belong to the
agent ».

Ensemble, les deux tests établissent la propriété visée : l'agent enregistre une
fois, la commande zéro fois.

**Ce qui n'est pas démontré.**

Premièrement, il n'y a pas de test qui compte les échantillons de *bout en bout*,
c'est-à-dire sur une exécution complète de `ocr review` ou `ocr scan` depuis la
ligne de commande. Les deux tests couvrent les deux moitiés séparément. Si un
troisième point d'enregistrement existait ailleurs dans la chaîne, aucun de ces
tests ne le verrait. J'ai vérifié par recherche textuelle qu'il n'y en a pas
aujourd'hui, mais rien n'empêche qu'il en réapparaisse un.

Deuxièmement, le test du compteur de remarques est asymétrique. Côté commande,
`TestEmitRunResult_RecordsNoRunMetrics` prouve bien qu'`emitRunResult` ne compte
plus. Mais il n'y a pas de test équivalent à
`TestRun_RecordsOneDurationSamplePerRun` pour vérifier que l'agent compte les
remarques exactement une fois. Cet appel n'a pas été modifié par la PR, donc ce
n'est pas une régression introduite ici, mais la propriété « exactement une fois »
n'est prouvée que pour la durée.

Troisièmement, le test de comptage de durée n'existe que pour `internal/scan`.
Le changement est identique dans `internal/agent`, et il est visible dans le
diff, mais il n'a pas son propre test. Un mainteneur peut légitimement demander
qu'on duplique `metrics_test.go` côté relecture de diff.

Quatrièmement, rien ne vérifie la *valeur* de la durée enregistrée, seulement le
nombre d'échantillons. Le changement de sémantique (durée de la dépêche → durée
de l'exécution complète) n'est donc pas testé, il est seulement documenté.

### 6. Limites et effets de bord

**La grandeur mesurée change.** Avant, l'histogramme recevait la durée de la
dépêche des sous-tâches, plus la durée totale mesurée par la commande. Maintenant
il reçoit la durée complète de `Run`, qui inclut l'analyse du diff ou
l'énumération des fichiers, le regroupement, la synthèse de projet et la
finalisation de la session. Les séries historiques d'un utilisateur ne sont donc
pas comparables avant/après. Il faut le dire dans les notes de version.

**Les exécutions échouées entrent maintenant dans la distribution.** C'est voulu,
mais cela déplace la médiane : une exécution qui échoue dès l'analyse du diff
dure quelques millisecondes et tire la distribution vers le bas. Un opérateur
qui surveille la durée médiane verra une marche sur son graphique.

**L'histogramme enregistre des secondes entières.**
`RecordReviewDuration` fait `mReviewDuration.Record(ctx, int64(dur.Seconds()))`
(`internal/telemetry/metrics.go:83`). La conversion en `int64` tronque : toute
exécution de moins d'une seconde enregistre 0. Ce n'est pas introduit par cette
PR, mais cela devient plus visible maintenant que les exécutions échouées, qui
sont souvent très rapides, sont comptées. Cela mériterait une PR séparée.

**`EnableMetricsForTest` est dans un fichier de production.** `testing.go` n'est
pas `testing_test.go`, donc il est compilé dans le binaire livré et
`EnableMetricsForTest` est une fonction exportée du paquet. Ce n'est pas rare en
Go pour un utilitaire destiné à d'autres paquets du même module — c'est la seule
manière, un fichier `_test.go` n'étant pas importable — mais c'est un point qui
se discute. Le paquet est `internal/`, donc la fonction n'est pas accessible hors
du module.

**`EnableMetricsForTest` n'est pas sûre en parallèle.** Elle écrit des variables
globales du paquet sans verrou et appelle `otel.SetMeterProvider`, qui est
global au processus. Deux tests qui l'utiliseraient avec `t.Parallel()` se
marcheraient dessus. Les tests ajoutés ne sont pas parallèles, donc le problème
ne se pose pas aujourd'hui, mais rien ne l'empêche demain.

### 7. Questions probables d'un mainteneur

**« Pourquoi l'agent plutôt que la commande comme propriétaire de la métrique ? »**
Parce que la commande ne voit pas toutes les exécutions. `ocr scan` retourne
avant `emitRunResult` quand `Run` échoue, donc les exécutions ratées ne seraient
jamais mesurées — or ce sont celles qu'on veut voir. L'agent est traversé par
toutes les exécutions, quel que soit le chemin de sortie, grâce au `defer` placé
en tête de `Run`.

**« Pourquoi capturer `ctx` dans `runCtx` ? »**
Parce que `ctx` est réaffecté plusieurs fois dans `Run`, chaque `StartSpan`
renvoyant un contexte enrichi. Une fermeture `defer` qui lirait `ctx`
enregistrerait la métrique dans le contexte de la dernière étape, pas de
l'exécution. En capturant la valeur de départ, la métrique est rattachée à
l'exécution entière.

**« La sémantique de la métrique change : n'est-ce pas une rupture ? »**
Si, et il faut le dire. La durée mesurée passe de « la dépêche » à « l'exécution
complète », et les exécutions échouées entrent désormais dans la distribution.
Les séries historiques ne sont pas comparables avant/après. Je pense que la
nouvelle définition est la bonne — c'est celle qu'un nom comme
`review.duration_seconds` suggère — mais elle mérite une ligne dans les notes de
version.

**« Pourquoi mettre un utilitaire de test dans un fichier non-test ? »**
Parce qu'un fichier `_test.go` n'est visible que de son propre paquet, et que les
tests qui en ont besoin sont dans `internal/scan` et `cmd/opencodereview`. Il
fallait donc une fonction exportée dans un fichier ordinaire. Le paquet est sous
`internal/`, donc elle reste inaccessible depuis l'extérieur du module. Si vous
préférez une autre forme — un sous-paquet `telemetrytest`, par exemple — c'est
facile à déplacer.

**« Le compteur de remarques est-il aussi prouvé « exactement une fois » ? »**
Non, pas complètement, et je préfère le dire. Il est prouvé que `emitRunResult`
ne le compte plus. Il n'y a pas de test qui vérifie que l'agent le compte
exactement une fois, contrairement à ce qui existe pour la durée. Cet appel
n'est pas modifié par la PR, mais je peux ajouter le test si vous le souhaitez.

**« Pourquoi n'y a-t-il de test de comptage que côté scan ? »**
Parce que le changement est strictement identique des deux côtés et que le test
côté scan est le moins coûteux à monter. C'est une lacune assumée : si vous
voulez la symétrie, dupliquer `internal/scan/metrics_test.go` vers
`internal/agent` est direct.

---

## PR #1435 — Supprimer des fonctions d'aide inutilisées dans `internal/agent`

**Branche** : `refactor/remove-dead-agent-helpers` · **Fichiers touchés** :
`internal/agent/util.go`, `internal/agent/util_test.go`

### 1. Le problème en une phrase

Quatre fonctions privées de `internal/agent` ne sont appelées par aucun code de
production ; trois d'entre elles sont des copies désormais divergentes de
fonctions qui vivent, elles, dans `internal/llmloop`.

### 2. Pourquoi ça compte

Ce n'est pas une correction de bug, et il ne faut pas le présenter comme telle.
C'est un nettoyage, et son intérêt est précis.

Le vrai risque est la *divergence silencieuse*. `buildMessageXML` existe en deux
exemplaires. Celle de `internal/llmloop/compression.go:209` est la version
vivante : elle sérialise aussi le champ `ReasoningContent` dans une balise
`<reasoning>`. Celle de `internal/agent/util.go` ne le fait pas. Quelqu'un qui
lit la version de `agent` en tire une idée fausse du format attendu par le
gabarit `MEMORY_COMPRESSION_TASK`. Même chose pour `copyMessages` : la version de
`llmloop` est une copie superficielle en une ligne, celle de `agent` recopiait
les champs un par un, avec un traitement particulier de `ToolCalls`. Deux
comportements, un seul nom.

S'ajoute un coût mesurable : quatre fonctions et quatre tests entretenus pour
rien, et une suite de tests qui gagne en crédibilité quand elle ne teste que du
code réellement utilisé. Un test vert sur du code mort donne une fausse
impression de couverture.

Enfin, ce n'est pas une chose que l'outillage aurait attrapée : la CI du projet
n'exécute que `go vet ./...` (`.github/workflows/ci.yml:74` et `:132`), et
`go vet` ne signale pas les fonctions inutilisées. Il n'y a pas de configuration
`golangci-lint` dans le dépôt, donc pas d'analyseur `unused`.

### 3. Comment le bug se produit

Il n'y a pas de bug d'exécution : rien n'est cassé, aucun utilisateur n'est
affecté. Ce qui s'est produit est une histoire de code.

Les quatre fonctions (`stripMarkdownFences`, `buildMessageXML`, `copyMessages`,
`countMessagesTokens`) vivaient dans `internal/agent/util.go` à l'époque où la
boucle d'appels au modèle et la compression de mémoire étaient dans le paquet
`agent`. Cette logique a ensuite été extraite dans `internal/llmloop`, qui a
emporté ses propres copies. Les originales sont restées, orphelines.

Leurs tests, eux, ont continué de passer. C'est le point important : un test
qui appelle directement une fonction privée maintient cette fonction
« utilisée » du point de vue du compilateur. Le compilateur Go signale une
*variable* locale inutilisée, jamais une fonction. Le code mort était donc
invisible, protégé par ses propres tests.

Depuis, les versions de `llmloop` ont évolué (`<reasoning>` ajouté dans
`buildMessageXML`, simplification de `copyMessages`) tandis que celles de
`agent` sont restées figées.

### 4. Ce que fait la correction

Elle supprime les quatre fonctions de `internal/agent/util.go`, les quatre tests
correspondants de `internal/agent/util_test.go`, et l'import
`internal/llm` devenu inutile dans les deux fichiers. Rien d'autre :
162 lignes retirées, zéro ligne ajoutée, aucun renommage, aucun déplacement.

**Pourquoi supprimer plutôt que dédupliquer.** On aurait pu faire pointer
`agent` vers les fonctions de `llmloop`. Ce serait à la fois plus de travail et
moins juste : `agent` n'en a pas besoin, donc créer une dépendance serait ajouter
un lien là où il n'y en a pas. Et les fonctions de `llmloop` sont privées à leur
paquet, sauf `StripMarkdownFences` qui a un alias exporté
(`internal/llmloop/compression.go:185`) — les exporter toutes pour un
consommateur qui n'existe pas serait élargir une interface sans raison.

**Comment on prouve qu'un code est vraiment mort — le point à défendre.**

C'est la question que posera le mainteneur, donc voici la démarche complète, et
pourquoi elle est exhaustive dans ce cas précis.

*Premier point : ces identifiants sont privés au paquet.* En Go, un identifiant
qui commence par une minuscule n'est visible que dans son propre paquet. Les
quatre noms commencent par une minuscule. Aucun autre paquet ne peut donc les
appeler, quelle que soit la façon dont il est écrit. La recherche peut se
limiter au paquet `internal/agent`, et cette limite est une garantie du langage,
pas une hypothèse.

*Deuxième point : la recherche textuelle est ici concluante.* En Go, il n'existe
pas de moyen d'appeler une fonction par son nom sous forme de chaîne :
`reflect` sait manipuler des méthodes exportées sur un type, pas des fonctions
libres privées, et il n'y a ni chargement dynamique ni `eval`. Un appel doit donc
apparaître littéralement dans le source. Un `git grep` sur le nom exact suffit :

```
git grep -n -E '\b(stripMarkdownFences|buildMessageXML|copyMessages|countMessagesTokens)\b' -- internal/agent/
```

Sur le commit parent, cette commande ne renvoie que deux familles de résultats :
les définitions dans `util.go` (lignes 93, 111, 126, 139) et les appels dans
`util_test.go`. Aucun appel depuis un fichier de production du paquet.

*Troisième point : il n'y a pas de source cachée.* Il fallait vérifier trois
choses pour que le grep soit complet. Aucun fichier du paquet n'a de directive
`//go:build`, donc il n'y a pas de variante compilée seulement sur une autre
plateforme qui utiliserait ces fonctions. Il n'y a pas de génération de code ni
de `go:generate` produisant du Go dans ce paquet. Et il n'y a pas de
`go:linkname`, qui permettrait un lien depuis un autre paquet.

*Quatrième point : les seuls appelants étaient les tests supprimés.* C'est ce qui
explique que le code ait survécu si longtemps, et c'est pourquoi la PR supprime
les tests en même temps. Garder les tests aurait rendu la compilation
impossible ; les garder en réécrivant les fonctions aurait été conserver le code
mort.

*Cinquième point : le paquet compile et ses tests passent après suppression.*
C'est la vérification finale. Si quoi que ce soit dans le paquet appelait encore
l'une des quatre fonctions, `go build ./internal/agent/` échouerait
immédiatement — le compilateur Go refuse un identifiant non défini. C'est une
preuve plus forte qu'un grep, parce qu'elle est faite par le compilateur
lui-même.

*Sixième point : l'import retiré est une confirmation.* Une fois les quatre
fonctions parties, l'import de `internal/llm` devient inutile dans `util.go` et
dans `util_test.go`. Or le compilateur Go *refuse* de compiler un fichier avec un
import inutilisé. Le fait que la PR ait dû retirer cet import prouve qu'aucune
autre ligne de ces deux fichiers n'utilisait le paquet `llm`. C'est un indice
gratuit fourni par le langage.

### 5. La preuve — ce qui est démontré et ce qui ne l'est pas

**Ce qui est démontré.**

Le compilateur fait l'essentiel du travail. Si un appel subsistait dans le
paquet, la compilation échouerait sur un identifiant non défini. Le fait que la
branche compile est donc une preuve directe qu'aucun code du paquet `agent`
n'appelle plus ces fonctions. La visibilité privée du langage étend cette preuve
à tout le module : aucun autre paquet ne pouvait les appeler.

La recherche textuelle sur l'ensemble du dépôt confirme la lecture du commit :
après suppression, les noms `stripMarkdownFences`, `buildMessageXML` et
`copyMessages` n'existent plus que dans `internal/llmloop` (et `copyMessages`
aussi dans `internal/session/history.go:387`, qui est une troisième
implémentation, indépendante et bien utilisée). `countMessagesTokens` n'existe
plus nulle part dans le dépôt.

Le reste de la suite de tests du paquet `agent` n'est pas touché et continue de
passer : rien d'autre ne dépendait de ce code.

**Ce qui n'est pas démontré.**

Premièrement, la couverture de tests globale baisse légèrement, et le projet a
un seuil. `.github/workflows/ci.yml:86-94` fait échouer la CI sous 90 %, et le
`Makefile` fixe le même `COVERAGE_THRESHOLD := 90`. Les lignes supprimées
étaient couvertes à 100 % par les tests supprimés ; retirer des lignes couvertes
d'un projet qui n'est pas à 100 % fait mécaniquement descendre le pourcentage.
L'effet est petit (une cinquantaine de lignes de production sur l'ensemble du
module) mais il est dans le mauvais sens, et je ne l'ai pas mesuré
précisément — il faut regarder le résultat de la CI.

Deuxièmement, rien ne prouve que personne ne *voulait* réintroduire ces
fonctions. Elles sont mortes aujourd'hui ; peut-être existe-t-il un travail en
cours, dans une autre branche ou une autre PR, qui comptait s'en servir. Le
compilateur ne peut rien dire là-dessus. Cela dit, `git revert` d'un commit qui
ne fait que supprimer est trivial, et les fonctions vivantes existent toujours
dans `llmloop`.

Troisièmement, la PR n'ajoute aucun test, par construction. Il n'y a rien de
nouveau à tester. La garantie de non-régression est celle du compilateur et de la
suite existante, pas d'un test dédié. C'est le contrat normal d'une PR de
suppression, mais il faut le formuler ainsi plutôt que de laisser croire qu'un
test couvre le changement.

Quatrièmement, je n'ai pas vérifié le comportement d'un éventuel outil externe
qui lirait ces symboles (un générateur de documentation, un analyseur tiers).
Comme ils sont privés, ce serait très surprenant, mais ce n'est pas vérifié.

### 6. Limites et effets de bord

**Aucun effet sur le comportement de l'outil.** Aucun chemin d'exécution ne
change. C'est la conséquence directe du fait que le code était mort.

**La couverture de tests baisse légèrement, et il y a un seuil à 90 %.** Voir la
section 5 : les lignes retirées étaient entièrement couvertes, donc le
pourcentage global descend un peu. Il faut regarder ce que dit la CI sur la PR.

**Le code reste dans l'historique git.** Rien n'est perdu définitivement. Un
`git show` sur le commit parent le fait réapparaître, et le `git revert` est
immédiat.

**Le doublon restant n'est pas traité.** Il subsiste deux implémentations de
`copyMessages` dans le dépôt, celle de `internal/llmloop/compression.go:230` et
celle de `internal/session/history.go:387`. Elles sont toutes deux utilisées, et
elles ne font pas la même chose (l'une est superficielle, l'autre profonde). La
PR ne les touche pas : ce serait un changement de comportement, pas un
nettoyage.

**Un mainteneur peut préférer le sens inverse.** Si l'intention du projet était
que `agent` finisse par réutiliser ces fonctions, il faudrait exporter celles de
`llmloop` plutôt que supprimer celles de `agent`. C'est une décision de
conception qui appartient aux mainteneurs, et je peux refaire la PR dans ce sens.

### 7. Questions probables d'un mainteneur

**« Comment savez-vous que ces fonctions ne sont appelées nulle part ? »**
Par trois moyens qui se recoupent. D'abord, les quatre noms commencent par une
minuscule : en Go, ils ne sont visibles que dans le paquet `internal/agent`,
donc la recherche peut s'y limiter et cette limite est une règle du langage.
Ensuite, un `git grep` sur les noms exacts dans ce paquet ne trouvait que les
définitions et les tests supprimés. Enfin, et c'est le plus solide, le paquet
compile après la suppression : le compilateur Go refuse tout identifiant non
défini, donc s'il restait un appel, la compilation échouerait.

**« Et par réflexion, ou par une chaîne de caractères ? »**
Ce n'est pas possible pour une fonction libre privée en Go. `reflect` accède aux
méthodes exportées d'un type, pas aux fonctions de paquet non exportées, et il
n'y a ni `eval` ni chargement dynamique. Un appel doit apparaître littéralement
dans le source, donc la recherche textuelle est concluante ici — ce qui ne serait
pas vrai dans un langage dynamique.

**« Y a-t-il des fichiers à contrainte de compilation qui pourraient les
utiliser ? »**
Non, j'ai vérifié : aucun fichier de `internal/agent` ni de `internal/llmloop`
n'a de directive `//go:build`. Il n'y a donc pas de variante compilée seulement
sur une autre plateforme que le grep aurait ratée. Il n'y a pas non plus de
génération de code dans ce paquet.

**« Pourquoi ne pas les faire pointer vers les versions de `llmloop` plutôt que
de les supprimer ? »**
Parce que `agent` n'en a pas besoin. Créer une dépendance de `agent` vers
`llmloop` pour du code que personne n'appelle ajouterait un lien sans gagner
quoi que ce soit. Et il faudrait exporter trois fonctions privées de `llmloop`
pour un consommateur qui n'existe pas.

**« Qu'est-ce qui garantit qu'on ne supprime pas quelque chose d'utile ? »**
Le fait que les versions vivantes existent toujours dans `internal/llmloop`, et
qu'elles y sont plus à jour : celle de `buildMessageXML` sérialise le champ
`ReasoningContent`, ce que la copie de `agent` ne faisait pas. Supprimer la copie
figée est précisément ce qui évite qu'on s'en serve un jour par erreur.

**« Cette PR ne devrait-elle pas venir avec un analyseur qui empêche que ça se
reproduise ? »**
Ce serait utile : aujourd'hui la CI n'exécute que `go vet ./...`, qui ne détecte
pas les fonctions inutilisées, et il n'y a pas de configuration
`golangci-lint` dans le dépôt. Ajouter l'analyseur `unused` attraperait ce genre
de cas automatiquement. Je ne l'ai pas inclus ici pour garder la PR à un seul
sujet, mais je peux ouvrir une PR distincte si cela vous intéresse.
