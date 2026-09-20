# Dossier de défense — Groupe B

> **Note de vérification.** Les messages d'échec cités dans ce dossier ont été reconstitués à partir du code des tests. Les résultats réellement observés en exécutant les tests sans leur correction sont consignés dans `preuves-execution.md` : citez ceux-là devant un mainteneur.


Projet : `alibaba/open-code-review` (Go). Cinq pull requests : #1473, #1474, #1476, #1477, #1432.

Ce dossier sert à défendre chaque changement devant les mainteneurs. Il n'ajoute rien qui ne soit pas dans le code ou les tests des branches concernées.

---

## Vocabulaire commun (à lire une fois)

Quelques notions reviennent dans plusieurs PR. Les voici en clair, une fois pour toutes.

**Modèle de langage et fenêtre de contexte.** L'outil envoie à un modèle de langage un texte (le « prompt ») et reçoit une réponse. Le modèle ne peut traiter qu'une quantité limitée de texte à la fois : c'est la fenêtre de contexte. On la mesure en *tokens*, des morceaux de mots (en gros, quelques caractères chacun). Dans ce projet, le plafond est configuré par `MaxTokens`.

**Budget de prompt.** Le projet ne remplit jamais la fenêtre à 100 %. La fonction `PromptTokenLimit` (`internal/llmloop/compression.go:29`) renvoie 80 % de `MaxTokens`. Au-delà, le code considère que la conversation est en danger. Un second seuil, à 60 %, déclenche une compression en tâche de fond.

**Tour d'outil (round).** L'agent de relecture ne fait pas un seul appel au modèle. Il boucle : le modèle demande un outil (lire un fichier, déposer un commentaire), le code exécute l'outil et renvoie le résultat, le modèle repart. Un « tour » = un message de l'assistant + les résultats d'outils qui le suivent. Dans le code, c'est le type `round` (`internal/llmloop/compression.go:35`) et la fonction `groupIntoRounds`.

**Compression de l'historique (trois zones).** Quand la conversation grossit trop, `partitionMessages` découpe l'historique en trois parties :
- la **zone gelée** : les deux premiers messages (`messages[0:2]`), c'est-à-dire la consigne système et le prompt initial qui contient les diffs. Elle n'est jamais supprimée ;
- la **zone à compresser** : les tours du milieu, résumés par un appel au modèle ;
- la **zone active** : les tours les plus récents, conservés tels quels.

**Groupe de fichiers (`FileGroup`).** Pour économiser des appels, l'agent peut relire plusieurs fichiers dans une même conversation. Un groupe a une clé, produite par `fileGroupKey` (`internal/agent/grouping.go:394`) : le chemin du fichier s'il est seul, sinon les chemins triés joints par des virgules (`"a.go,b.go"`). Cette clé sert de `taskKey` dans la boucle d'outils.

**Hunk.** Un diff est découpé en blocs appelés *hunks*, introduits par une ligne `@@ -1,5 +1,2 @@`. Chaque hunk a un « côté ancien » (lignes de contexte + lignes supprimées) et un « côté nouveau » (lignes de contexte + lignes ajoutées).

**Placement d'un commentaire.** Le modèle renvoie un commentaire avec un extrait de code (`existing_code`) mais pas de numéro de ligne. Le code retrouve le numéro en cherchant cet extrait dans le diff : c'est `ResolveComment` (`internal/diff/resolver.go:62`).

---

## PR #1473 — La compression de l'historique ignorait la zone gelée et effaçait le dernier tour

**Branche** : `fix/compression-frozen-zone` · **Fichiers touchés** : `internal/llmloop/compression.go`, `internal/llmloop/compression_test.go`, `internal/scan/agent_test.go`

### 1. Le problème en une phrase

Quand la compression décide combien de tours récents garder, elle ne compte pas les tokens de la zone gelée, qu'elle garde pourtant toujours ; et dans le cas où tout tient, elle résume au contraire *tout*, y compris le tour que le modèle n'a pas encore vu.

### 2. Pourquoi ça compte

Deux conséquences visibles.

La première : la relecture s'arrête pour rien. Quand la compression n'a pas assez réduit l'historique, `addNextMessage` renvoie `false`, la boucle sort avec `StopCompression` (`internal/llmloop/loop.go:518`) et l'utilisateur lit `stopped because context compression exceeded its threshold` (`MainLoopStop.Reason`, `internal/llmloop/loop.go:348`). Or les fichiers du groupe qui n'ont encore aucun commentaire sont alors marqués en échec. Le test `TestAddNextMessage_CompressionAccountsForFrozenZone` reproduit la situation avec des chiffres concrets : `MaxTokens` = 10 000, donc un budget de prompt de 8 000 ; une zone gelée d'environ 5 000 tokens (un prompt de groupe portant de gros diffs) ; huit tours d'environ 1 400 tokens chacun. Avant la correction, la compression conservait cinq tours (≈ 7 000 tokens) parce qu'elle raisonnait sur les 8 000 complets, oubliant les 5 000 de la zone gelée : total ≈ 12 000, toujours au-dessus de 8 000, donc arrêt. Après, elle n'en garde que deux (≈ 2 800), le total revient sous 8 000 et la relecture continue.

La seconde : une perte d'information. Dans le cas « tout tient », l'ancien code mettait `activeCount = 0` et `compressEnd = len(messages)`, ce qui veut dire « résume l'intégralité de l'historique ». Le tour qui vient d'être exécuté — l'appel d'outil et son résultat que le modèle n'a pas encore lus — était remplacé par un résumé. Concrètement : l'agent demande à lire un fichier, le fichier est lu, et le contenu lu est effacé avant même que le modèle le voie.

### 3. Comment le bug se produit

Le mécanisme, étape par étape.

1. `addNextMessage` (`internal/llmloop/loop.go:834`) est appelé à chaque fin de tour. Il ajoute le message de l'assistant et les résultats d'outils, puis compte le total avec `CountMessagesTokens`.
2. Si le total dépasse 80 % de `MaxTokens`, il appelle `runCompression` (`internal/llmloop/compression.go:240`).
3. `runCompression` appelle `partitionMessages(msgs, MaxTokens, 0)` pour savoir quoi résumer.
4. `partitionMessages` appelait `computeActiveZoneSize(rounds, messages, maxTokens, prevSummaryTokenEstimate)`. Dans ce chemin, `prevSummaryTokenEstimate` vaut `0`. Le budget disponible était donc `PromptTokenLimit(maxTokens) - 0`, soit les 8 000 en entier.
5. Mais la zone gelée (`messages[0:2]`) est reconstruite à l'identique par `runCompression` : `rebuilt := make([]llm.Message, 2); copy(rebuilt, msgs[:2])` (`internal/llmloop/compression.go:301`). Elle est donc *toujours* conservée, et ses tokens n'étaient comptés nulle part. Le résultat reconstruit pouvait rester au-dessus de la limite.
6. C'est particulièrement grave ici parce que la zone gelée n'est pas petite : le second message est le prompt de la tâche principale, qui contient les diffs de tout le groupe. Elle grossit aussi au fil du temps, puisque chaque résumé produit y est ajouté (`currentText + "\n\n<previous_review_summary>…"`, ligne 306).
7. Séparément, la branche « tout tient » (`if result.activeCount >= len(result.rounds)`) portait le commentaire « no compression needed » mais faisait l'inverse : elle forçait `compressEnd = len(messages)` et `activeCount = 0`. Et `runCompression` ne s'arrête que si `compressEnd <= frozenEnd` (ligne 247), condition fausse ici. Tout était donc bien résumé. Ce chemin est celui de la compression asynchrone, déclenchée au seuil doux de 60 % par `triggerAsyncCompression`, là où justement tout tient encore.

### 4. Ce que fait la correction

Trois changements dans `partitionMessages`, tous locaux à ce fichier.

**a. On facture la zone gelée.** Avant d'appeler `computeActiveZoneSize`, le code calcule :

```go
reserved := CountMessagesTokens(messages[:result.frozenEnd]) + prevSummaryTokenEstimate
```

Le paramètre s'appelait déjà `reservedTokens`. Le changement consiste à y mettre ce qui est réellement réservé, c'est-à-dire tout ce qui sera gardé en plus des tours actifs. Le commentaire de `computeActiveZoneSize` a été réécrit pour le dire. Autre approche possible : soustraire la zone gelée à l'intérieur de `computeActiveZoneSize`. Elle a été écartée parce que cette fonction reçoit déjà `rounds` et `messages` séparément et n'a pas à connaître la convention « la zone gelée fait deux messages » ; le paramètre `reservedTokens` existait précisément pour que l'appelant décide.

**b. Le dernier tour est protégé.** La branche « tout tient » ne met plus `activeCount = 0` mais `activeCount = 1` : on résume tous les tours sauf le plus récent. Un garde-fou suit : si `activeStartIdx == 0`, c'est-à-dire s'il n'y a qu'un seul tour et qu'il est protégé, alors `compressEnd = frozenEnd` et `runCompression` sort immédiatement sans appeler le modèle.

**c. Repli pour le dernier tour trop gros.** Si `computeActiveZoneSize` renvoie `0` — même le dernier tour ne rentre pas à côté de la zone gelée — le code met `compressEnd = len(messages)` et résume tout, y compris ce dernier tour. Le commentaire est explicite : « lossy but better than stopping the review ». Alternative écartée : conserver quand même le tour et laisser `addNextMessage` renvoyer `false`. Cela aurait transformé un seul résultat d'outil volumineux en arrêt définitif de la relecture du groupe, ce que la PR cherche justement à éviter.

### 5. La preuve

Cinq tests ajoutés dans `internal/llmloop/compression_test.go`, plus deux tests existants adaptés.

- `TestPartitionMessages_ChargesFrozenZone` : construit une zone gelée d'environ 5 000 tokens et huit tours d'environ 1 400 tokens, avec `MaxTokens` = 10 000. Il recalcule ce que la compression conserverait réellement — `CountMessagesTokens(messages[:frozenEnd]) + CountMessagesTokens(messages[compressEnd:])` — et échoue si c'est au-dessus de `PromptTokenLimit(10000)`. Sur `main`, ce total vaut environ 12 000 et le message d'échec l'affiche avec `compressEnd` et `activeCount`. C'est une preuve directe : il mesure l'invariant que la fonction est censée garantir, pas un détail d'implémentation.
- `TestAddNextMessage_CompressionAccountsForFrozenZone` : même situation, mais au niveau de `addNextMessage`. Il échoue avec `conversation stopped at N tokens although compressing older rounds fits the limit`. C'est ce test qui relie le défaut à la conséquence utilisateur (`StopCompression`).
- `TestPartitionMessages_EverythingFitsKeepsMostRecentRound` et `TestAddNextMessage_KeepsCurrentRound` : couvrent la protection du dernier tour. Le second vérifie que les deux derniers messages sont bien l'appel d'outil `c9` et son résultat, et non un résumé. Sur `main`, ils ont disparu.
- `TestPartitionMessages_OversizedLatestRoundIsCompressible` et `TestAddNextMessage_OversizedLatestRoundContinues` : couvrent le repli. Le dernier tour porte un résultat d'environ 4 000 tokens, au-dessus des 3 000 restants. Le test exige que la conversation continue *et* que le modèle de compression ait bien été appelé (`len(client.requests) == 0` déclenche un échec) — sans quoi « ça continue » pourrait être obtenu par accident.
- `TestPartitionMessages_EverythingFits` (existant) est mis à jour : il attendait `activeCount = 0` et `compressEnd = len(messages)`, il attend maintenant `activeCount = 1` et `compressEnd = frozenEnd`. C'est le changement de comportement assumé, écrit noir sur blanc.

### 6. Limites et effets de bord

- **Le dernier tour trop gros reste traité en repli, donc en perte de contenu.** Si le résultat d'outil du dernier tour ne tient pas à côté de la zone gelée, il est résumé comme les autres. Le modèle ne verra donc jamais le texte brut de ce qu'il a demandé, seulement un résumé. C'est un choix : continuer en dégradé plutôt que s'arrêter. Le cas est couvert par deux tests et par un commentaire dans le code, pas par une optimisation (on ne tronque pas, on ne découpe pas le résultat).
- **Si la zone gelée dépasse à elle seule le budget, rien ne change.** `computeActiveZoneSize` renvoie `0`, tout est résumé, mais la zone gelée reconstruite reste au-dessus de la limite et `addNextMessage` renvoie toujours `false`. Ce cas est censé être attrapé plus tôt, par `checkPromptBudget` côté agent.
- **La compression asynchrone récupère moins de tokens qu'avant.** Puisqu'on garde toujours au moins le dernier tour, le gain est plus faible. Et s'il n'y a qu'un seul tour, la compression ne fait plus rien du tout — elle sort sans appeler le modèle. C'est exactement ce qui a forcé la modification de `internal/scan/agent_test.go` : ce test pré-existant provoquait le dépassement du seuil doux dès le premier tour, et comptait sur la compression du tour unique. Il a fallu lui donner un second tour, pour qu'il reste un tour plus ancien à résumer. Aucune ligne de production n'a changé dans `internal/scan`.
- **Changement de comportement pour les utilisateurs existants** : les conversations qui s'arrêtaient en `StopCompression` iront plus loin, et consommeront donc plus de tokens. Un utilisateur qui pilote son coût par `MaxTokensBudget` verra ce budget atteint plus souvent qu'avant, ce qui est un arrêt mieux classé (`StopTokenBudget`, budget déclaré) mais reste un arrêt.

### 7. Questions probables d'un mainteneur

**« Pourquoi avoir choisi de toujours garder le dernier tour, plutôt que de laisser la compression décider ? »**
Parce que le dernier tour contient le résultat d'outil que le modèle vient de demander et n'a pas encore lu. Le résumer revient à répondre à une question par « voici un résumé de ta question ». En pratique, le modèle redemande la même chose au tour suivant, ce qui coûte un tour de plus et peut boucler. Le garder est la seule façon de rendre le tour utile.

**« La zone gelée est comptée à chaque appel de `partitionMessages`, sur un prompt qui peut contenir de gros diffs. Quel est le coût ? »**
`CountMessagesTokens` ne s'applique ici qu'aux deux premiers messages, pas à tout l'historique, et `partitionMessages` n'est appelé qu'au moment d'une compression, pas à chaque tour. Le comptage lui-même utilise le même tokenizer que le reste du code. Le surcoût est du même ordre que celui qu'on payait déjà pour compter les tours.

**« `activeCount = 1` dans la branche "tout tient" : n'est-ce pas une régression, puisqu'on compresse maintenant dans un cas où on ne compressait pas ? »**
Non, c'est l'inverse. L'ancien code affichait « no compression needed » mais posait `compressEnd = len(messages)`, ce qui compressait tout. Le nouveau code compresse tout *sauf* le dernier tour. On compresse donc strictement moins qu'avant dans ce chemin. Le cas où l'on ne compresse vraiment rien est traité juste après, par le garde `activeStartIdx == 0`.

**« Le repli du dernier tour trop gros perd des données. Pourquoi ne pas tronquer le résultat d'outil à la place ? »**
Tronquer demanderait de décider où couper un résultat d'outil arbitraire (contenu de fichier, sortie de commande), et un point de coupure mal choisi produit du texte trompeur. Le résumé passe par le modèle, qui sait au moins ce qu'il garde. Cela dit, cette PR ne prétend pas régler le problème du résultat d'outil trop volumineux ; elle se contente de ne plus en faire un arrêt.

**« Pourquoi avoir modifié un test dans `internal/scan` alors que la PR touche `internal/llmloop` ? »**
Parce que ce test dépendait du comportement corrigé : il ne créait qu'un seul tour et attendait que la compression asynchrone le résume. Maintenant que le dernier tour est protégé, un tour unique n'est plus compressible. Le test a été ajusté pour créer un tour plus ancien. Le code de production de `internal/scan` n'est pas touché, et le commentaire du test explique pourquoi.

**« Comment savoir que `reserved` n'est pas compté deux fois, puisque la zone gelée accumule déjà les résumés précédents ? »**
`prevSummaryTokenEstimate` sert à réserver de la place pour le résumé *à venir*, qui n'est pas encore dans les messages. `CountMessagesTokens(messages[:frozenEnd])` mesure ce qui est déjà là, résumés passés compris. Les deux termes portent sur des choses différentes. Le commentaire de `computeActiveZoneSize` a été réécrit pour lever exactement cette ambiguïté.

---

## PR #1474 — Le suivi par fichier perdait des fichiers entre les tours, les renommages et les commentaires sans chemin

**Branche** : `fix/agent-per-file-tracking` · **Fichiers touchés** : `internal/agent/agent.go`, `internal/agent/per_file_tracking_test.go` (nouveau), `internal/llmloop/loop.go`, `internal/llmloop/loop_execute_more_test.go`

### 1. Le problème en une phrase

Trois chemins différents font qu'un fichier réellement relu est compté comme non relu : un tour de relecture ultérieur qui s'arrête annule la réussite d'un tour précédent ; un commentaire déposé sous l'ancien nom d'un fichier renommé n'est jamais retrouvé sous le nouveau ; et un commentaire sans chemin, dans un groupe de plusieurs fichiers, est classé sous la clé du groupe, qui n'est le nom d'aucun fichier.

### 2. Pourquoi ça compte

Tout le suivi par fichier se fait par `CommentCollector.CommentsForPath(d.NewPath)`. Cette clé est utilisée à quatre endroits : le filtre de relecture, le bloc des commentaires déjà confirmés injecté au tour suivant, le calcul de couverture, et la décision `markCompleted` / `markFailed` (`internal/agent/agent.go:773-818`).

Quand un fichier est marqué en échec, deux choses se produisent. D'abord le manifeste de la run le classe `failed`, ce qui peut faire basculer tout le run en `terminal_state=failed`. Ensuite, et surtout, `RecordReviewItemFailed` écrit une ligne `review_item_failed` dans le journal de session ; à la reprise, `applyResumeLine` fait `delete(s.Items, rec.Fingerprint)` (`internal/session/resume.go:184-187`). Le fichier est donc **relu de zéro au prochain `--resume`**, avec le coût en tokens correspondant, alors qu'il avait bien été relu.

Le cas du commentaire sans chemin est encore plus direct : le commentaire est déposé sous un chemin comme `"a.go,b.go"`. Aucun fichier ne porte ce nom. Le commentaire n'apparaît dans aucune vue par fichier, il ne compte pour aucune couverture, et il porte dans la sortie un chemin qui n'existe pas dans le dépôt.

### 3. Comment le bug se produit

**Chemin 1 — le tour ultérieur qui s'arrête.** `executeGroupSubtask` (`internal/agent/agent.go:1374`) boucle sur `maxRounds` tours de relecture. Un booléen local `completed` passe à `true` dès qu'un tour se termine par `task_done`. Le code traitait déjà correctement le cas « un tour ultérieur renvoie une erreur Go » : il enregistre un avertissement `review_round_failed` et fait `break`, gardant les résultats acquis. Mais le cas « un tour ultérieur s'arrête sans erreur » (par exemple `StopMaxRounds`, le plafond de tours d'outils) tombait dans une autre branche, qui posait `lastStop = &subtaskStop{…}` puis `break`. À la fin, `if lastStop != nil { return false, lastStop, nil }` — le `completed = true` du tour précédent était écrasé. L'appelant voyait `!completed` avec un `stop`, passait dans la classification par fichier, et tout fichier du groupe sans commentaire était marqué en échec. Une asymétrie, donc : l'erreur pardonnait, l'arrêt non.

**Chemin 2 — le fichier renommé.** `Agent.findDiff` (`internal/agent/agent.go:2182`) répond aussi bien sur `OldPath` que sur `NewPath`. Quand le modèle dépose un commentaire sous l'ancien nom (ce qui arrive : l'ancien nom apparaît dans l'en-tête du diff), `DiffLookup(cm.Path)` trouve le bon diff, `ResolveComment` place correctement le commentaire, et `CommentCollector.Add` l'enregistre sous `cm.Path`, donc sous l'**ancien** nom. Mais tous les lecteurs interrogent `CommentsForPath(d.NewPath)`. Le commentaire est stocké, résolu, correct — et invisible. Le fichier passe pour non couvert.

**Chemin 3 — le commentaire sans chemin.** `tool.ParseCommentsWithPath(args, taskKey)` (`internal/llmloop/loop.go:680`) utilise `taskKey` comme chemin de secours quand le modèle omet `path`. Pour un groupe d'un seul fichier, `fileGroupKey` renvoie le chemin de ce fichier : le secours est correct. Pour un groupe de plusieurs fichiers, il renvoie `"a.go,b.go"`. Le commentaire part alors avec ce chemin ; `DiffLookup` ne trouve rien ; la recherche inter-fichiers `RelocateAcrossFiles` peut le sauver si l'extrait de code est trouvable ailleurs ; sinon il était quand même ajouté au collecteur sous `"a.go,b.go"`.

### 4. Ce que fait la correction

**a. `internal/agent/agent.go`, dans `executeGroupSubtask`** — sept lignes ajoutées, juste après `classifyMainLoopStop` :

```go
if completed {
    a.recordWarning("review_round_failed", groupKey, fmt.Sprintf("round %d: %s", round, reason))
    fmt.Fprintf(stdout.Writer(), "[ocr] Round %d stopped for group %q: %s (keeping earlier findings)\n", …)
    break
}
```

Le choix est d'aligner la branche « arrêt » sur la branche « erreur » déjà présente juste au-dessus : même avertissement `review_round_failed`, même message « keeping earlier findings », même `break`. On ne perd pas l'information — l'arrêt reste visible dans les avertissements et dans la sortie — mais il ne requalifie plus un travail déjà fait. Alternative écartée : ne pas toucher `executeGroupSubtask` et corriger l'appelant pour qu'il devine. L'appelant ne dispose pas de `completed` par tour, seulement du résultat global ; l'information n'existe qu'ici.

**b. `internal/llmloop/loop.go`, renommage** — quatre lignes, placées juste après le `DiffLookup` et avant toute résolution :

```go
if d != nil && cm.Path == d.OldPath && d.NewPath != "" && d.NewPath != "/dev/null" {
    cm.Path = d.NewPath
}
```

Le test ne porte pas sur `d.IsRenamed` mais sur « le chemin du commentaire est l'ancien chemin de ce diff ». C'est plus général et sans effet pour un fichier non renommé, où `OldPath == NewPath` rend l'affectation neutre. La garde `!= "/dev/null"` évite de re-clé un commentaire vers le marqueur de fichier supprimé. Le re-clé est fait **avant** la résolution pour que tout ce qui suit — la recherche inter-fichiers, la tâche de re-localisation LLM, la session de fichier ouverte par `GetOrCreateFileSession(cm.Path)` — utilise déjà le bon chemin.

**c. `internal/llmloop/loop.go`, commentaire sans chemin** — après la recherche inter-fichiers :

```go
if d == nil && !located && r.deps.DiffLookup != nil && cm.Path == taskKey {
    r.RecordWarning("comment_dropped", taskKey, "comment without a path matched no single reviewed file; dropped")
    continue
}
```

Quatre conditions, chacune nécessaire. `d == nil` : aucun diff ne porte ce chemin, donc ce n'est pas un vrai fichier — ce qui exclut naturellement le groupe d'un seul fichier, où `DiffLookup(taskKey)` trouve le diff. `!located` : la recherche inter-fichiers n'a pas su le placer. `cm.Path == taskKey` : le chemin vient bien du secours, pas d'un chemin inventé par le modèle. `r.deps.DiffLookup != nil` : un appelant qui n'a pas câblé de recherche de diff ne doit pas voir ses commentaires supprimés. Alternative écartée, visible dans le commentaire du code : classer quand même le commentaire sous la clé du groupe. C'était le comportement d'avant, et il produit un chemin qu'aucun diff ne porte.

### 5. La preuve

Trois tests dans le nouveau fichier `internal/agent/per_file_tracking_test.go`, deux dans `internal/llmloop/loop_execute_more_test.go`.

- `TestExecuteGroupSubtask_LaterRoundStopKeepsCompletion` : scénarise deux tours. Le premier dépose un commentaire sur `a.go` puis appelle `task_done`. Le second n'appelle jamais `task_done` et épuise son plafond de tours d'outils. Le test exige `done == true` et `stop == nil`, et en plus la présence de l'avertissement `review_round_failed`. Sur `main` il échoue sur `round 1 completed; got done=false stop=…`. Le second contrôle est important : il empêche de « réparer » le test en avalant silencieusement l'arrêt.
- `TestDispatchSubtasks_RenamedFileCommentOnOldPath` : un seul diff, `old.go` → `new.go`, `IsRenamed: true`. Le modèle dépose son commentaire sur `old.go`. Le test vérifie quatre choses : le `Path` du commentaire retourné vaut `new.go`, sa ligne vaut 2 (donc la résolution a bien fonctionné, on n'a pas juste réécrit un chemin), `CommentsForPath("new.go")` en trouve un, et il n'y a **pas** d'avertissement `subtask_error`. Ce dernier point est la preuve de la conséquence : sur `main`, `new.go` était déclaré en échec bien qu'ayant un commentaire.
- `TestExecuteToolCall_CodeCommentRenamedOldPath` : la même chose au niveau de la boucle d'outils, avec un `DiffLookup` qui répond sur les deux chemins, comme le fait `findDiff` en production.
- `TestExecuteGroupSubtask_PathlessCommentInGroup` : trois sous-cas dans un groupe de deux fichiers. Un commentaire sans `path` mais avec un `existing_code` présent dans `a.go` doit atterrir sur `a.go:2` (il est sauvé par la recherche inter-fichiers). Un commentaire sans `path` et sans code localisable doit être supprimé, avec l'avertissement `comment_dropped`. Et, cas de non-régression, un groupe d'un seul fichier doit continuer à utiliser le chemin de secours. Ce troisième sous-cas est ce qui prouve que la correction ne casse pas le mode le plus courant.
- `TestExecuteToolCall_CodeCommentGroupKeyFallbackDropped` : envoie deux commentaires dans le même appel, un plaçable et un non plaçable, avec `taskKey = "a.go,b.go"`. Vérifie qu'il ne reste que le plaçable, et que l'avertissement est bien attribué au groupe.

### 6. Limites et effets de bord

- **Des commentaires sont désormais supprimés alors qu'ils étaient conservés.** C'est un changement visible. Un commentaire général sur un changement, sans extrait de code identifiable, dans un groupe de plusieurs fichiers, disparaît de la sortie. L'avertissement `comment_dropped` le signale, mais il faut lire les avertissements. On peut défendre que ce commentaire était déjà inutilisable, puisqu'il pointait un chemin inexistant ; reste que quelqu'un qui lisait la liste brute des commentaires en verra moins.
- **La perte ne concerne que les groupes de plusieurs fichiers.** Le mode un-fichier-par-groupe, le mode `scan` (où `lookupDiff` répond sur le chemin de l'item) et les commentaires portant un `path` explicite ne sont pas touchés.
- **La correction du renommage ne couvre pas tout.** Si le modèle dépose un commentaire sous un troisième chemin, ni l'ancien ni le nouveau, on retombe sur la recherche inter-fichiers puis sur la re-localisation par le modèle, inchangées.
- **La correction du tour ultérieur change la classification d'échec.** Un groupe dont le tour 2 s'arrête sur le plafond de tours ne remontera plus de `subtaskStop`. Si un utilisateur s'appuyait sur `terminal_state=failed` pour détecter ces arrêts en CI, il devra désormais lire les avertissements `review_round_failed`. Le signal n'a pas disparu, il a changé de canal.
- **Les trois correctifs sont indépendants.** Ils partagent le sujet « la clé par fichier » mais pourraient être trois PR. Si un mainteneur le demande, ils se séparent proprement : `agent.go` d'un côté, les deux blocs de `loop.go` de l'autre.

### 7. Questions probables d'un mainteneur

**« Pourquoi trois correctifs dans une seule PR ? »**
Parce qu'ils ont la même cause : tout le suivi par fichier lit `CommentsForPath(d.NewPath)`, et chacun de ces trois chemins produit une clé qui n'est pas `NewPath`. Pris séparément, chaque correctif laisse un fichier relu compté comme non relu. Cela dit, ils sont indépendants dans le code et je peux les séparer si vous préférez trois revues plus courtes.

**« Supprimer un commentaire est une perte de donnée. Pourquoi ne pas l'attacher au premier fichier du groupe ? »**
Parce que ce serait une affirmation fausse sur un fichier précis. Le commentaire finirait dans la revue du fichier `a.go` alors que rien ne dit qu'il le concerne, et le lecteur ne pourrait pas faire la différence avec un vrai constat. Un avertissement `comment_dropped` est honnête : il dit qu'un commentaire est arrivé sans qu'on sache où le mettre. Si vous préférez une autre issue, la conserver dans un canal « commentaires de groupe » distinct serait cohérent, mais cela demande une structure de sortie qui n'existe pas aujourd'hui.

**« La condition de suppression a quatre termes. Lesquels sont vraiment nécessaires ? »**
Les quatre. `cm.Path == taskKey` limite au chemin de secours. `d == nil` exclut le groupe d'un seul fichier, où le secours est un vrai fichier. `!located` laisse passer les commentaires que la recherche inter-fichiers a sauvés. `DiffLookup != nil` protège les appelants qui n'ont pas de recherche de diff câblée et pour qui `d == nil` ne veut rien dire. Retirer n'importe lequel supprime des commentaires valides — c'est ce que vérifie le sous-cas « single-file group keeps the fallback path ».

**« Le re-clé du renommage ne teste pas `IsRenamed`. Est-ce volontaire ? »**
Oui. La condition réelle est « le diff trouvé répond sous son ancien chemin », ce qui est exactement `cm.Path == d.OldPath`. Tester `IsRenamed` ajouterait une dépendance à un champ que le producteur de diffs doit renseigner correctement, sans rien gagner : pour un fichier non renommé, `OldPath == NewPath` et l'affectation ne fait rien.

**« Un tour ultérieur qui s'arrête ne devrait-il pas rester un signal d'échec ? »**
Il le reste, mais comme avertissement, pas comme échec du groupe. Le code faisait déjà ce choix pour une erreur Go dans un tour ultérieur, juste au-dessus. La correction n'invente pas une politique, elle applique la politique existante au cas voisin. Et l'argument de fond est que marquer en échec entraîne `RecordReviewItemFailed`, donc une re-relecture complète à la reprise, ce qui est cher pour un travail déjà fait.

**« Un fichier avec zéro commentaire dans un groupe qui a réussi : comment le distinguer d'un fichier non relu ? »**
On ne peut pas le distinguer par les commentaires seuls, et c'était déjà le cas avant cette PR : quand le groupe se termine par `task_done`, tous ses fichiers sont marqués `markCompleted` même sans commentaire (`internal/agent/agent.go:812-817`). Zéro commentaire veut dire « rien à signaler ». La correction rend simplement ce raisonnement disponible quand c'est un tour *ultérieur* qui s'arrête.

---

## PR #1476 — Le découpage des groupes par budget de tokens ignorait le poids du prompt

**Branche** : `fix/group-budget-prompt-overhead` · **Fichiers touchés** : `internal/agent/agent.go`, `internal/agent/grouping.go`, `internal/agent/grouping_test.go`

### 1. Le problème en une phrase

`enforceGroupTokenBudget` décidait de découper un groupe en additionnant les tokens des diffs seuls, alors que le contrôle qui rejette le groupe plus tard, `checkPromptBudget`, mesure le prompt complet — diffs plus modèle de message, règles système fusionnées et liste des autres fichiers modifiés.

### 2. Pourquoi ça compte

Les deux mesures ne sont pas comparées au même moment, mais elles sont comparées à la **même** limite : `llmloop.PromptTokenLimit(MaxTokens)`, soit 80 % de `MaxTokens`. Un groupe dont les diffs pèsent juste en dessous de cette limite passait donc la barrière du découpage, puis échouait au tour 1 sur la barrière du prompt.

Le test ajouté chiffre exactement le cas : `MaxTokens` = 10 000, donc une limite de 8 000 ; deux diffs de 3 900 tokens chacun, soit 7 800, sous la limite ; et un modèle de message système de 400 tokens autour. Le total rendu vaut environ 8 200. L'ancien code gardait le paquet, et `checkPromptBudget` le rejetait avec `prompt tokens (…) exceed 80% of max_tokens(10000) [round 1]`.

Et le rejet n'est pas anodin. `checkPromptBudget` renvoie un `subtaskStop` de classe `session.FailureBudget`. Au tour 1, `executeGroupSubtask` fait `return false, stop, nil` : aucun fichier du groupe n'a de commentaire, donc **tous** sont marqués en échec et enregistrés par `RecordReviewItemFailed`. Deux fichiers qui tenaient largement chacun de leur côté ne sont pas relus du tout, et un avertissement `token_threshold_exceeded` est émis.

### 3. Comment le bug se produit

1. `dispatchSubtasks` (`internal/agent/agent.go:657`) appelle `groupDiffs` avec `llmloop.PromptTokenLimit(a.args.Template.MaxTokens)` comme `tokenLimit`.
2. Selon la stratégie décidée par `Template.GroupingPlan`, les fichiers sont regroupés soit par le modèle, soit en un paquet unique (`groupWithoutLLM`, branche `GroupingBundleAll`).
3. Dans les deux cas, `enforceGroupTokenBudget(groups, tokenLimit)` sert de soupape : elle éclate en groupes d'un fichier tout groupe trop lourd.
4. L'ancienne mesure était :
   ```go
   total := int64(0)
   for _, d := range g.Diffs { total += int64(llm.CountTokens(d.Diff)) }
   if total <= int64(tokenLimit) { … }
   ```
   Uniquement `d.Diff`. Rien d'autre.
5. Plus tard, `executeGroupSubtask` rend le vrai prompt via `buildMainTaskMessages(rule, changeFilesExcludingGroup, concatenatedDiffs, roundPlan, confirmedText)`. Ce prompt ajoute autour des diffs : le texte du modèle de message lui-même, `{{system_rule}}` (les règles système fusionnées pour tous les fichiers du groupe, via `resolveGroupSystemRule`), `{{change_files}}` (la liste des autres fichiers modifiés hors groupe), `{{requirement_background}}`, la date. Et `buildConcatenatedDiffs` enrobe chaque diff dans du XML par fichier.
6. `checkPromptBudget` compte tout cela avec `llmloop.CountMessagesTokens(messages)` et le compare à `PromptTokenLimit(MaxTokens)`. Deux mesures différentes, une seule limite : l'écart est exactement le surcoût du prompt.

### 4. Ce que fait la correction

On rend la mesure du découpage identique à celle du contrôle. `enforceGroupTokenBudget` reçoit un nouveau paramètre, un type fonction :

```go
type groupTokenCounter func(diffs []model.Diff) int
```

Et côté agent, l'implémentation rend le prompt du tour 1 tel qu'il sera réellement construit :

```go
func (a *Agent) groupPromptTokens(diffs []model.Diff) int {
    messages := a.buildMainTaskMessages(a.resolveGroupSystemRule(diffs), a.buildChangeFilesExceptGroup(diffs),
        buildConcatenatedDiffs(diffs), "", "")
    return llmloop.CountMessagesTokens(messages)
}
```

Trois points de conception à défendre.

**Pourquoi une fonction injectée et non un appel direct ?** Parce que `enforceGroupTokenBudget` vit dans `grouping.go` et n'a pas d'`Agent`. Elle est aussi appelée par des tests et par `groupWithoutLLM`, qui n'ont pas forcément de modèle de message à rendre. L'injection garde le découpage testable seul.

**Pourquoi un repli `rawDiffTokens` quand la fonction est `nil` ?** Pour ne pas casser les appelants sans modèle de message, et pour que les tests existants continuent de décrire le découpage brut. Le repli est l'ancienne mesure, extraite telle quelle dans une fonction nommée.

**Pourquoi rendre le prompt plutôt qu'ajouter une marge fixe ?** Une marge fixe (par exemple « réserver 10 % ») aurait été plus simple, mais elle est fausse dans les deux sens : trop petite pour un groupe avec beaucoup de règles fusionnées et une longue liste de fichiers, trop grande pour un petit changement — ce qui découperait des groupes qui tenaient, et ferait perdre le bénéfice du regroupement (une conversation partagée au lieu d'une par fichier). Rendre le prompt donne le nombre exact que `checkPromptBudget` recalculera.

Le commentaire de la fonction documente le seul écart connu : la guidance de plan n'est pas encore connue au moment du regroupement et est donc laissée vide.

### 5. La preuve

`TestDispatchSubtasks_BundleSplitAccountsForPromptOverhead` dans `internal/agent/grouping_test.go`. Il n'appelle pas `enforceGroupTokenBudget` directement, il passe par `dispatchSubtasks`, c'est-à-dire par le vrai chemin.

Le montage est choisi pour que l'ancienne mesure et la nouvelle tombent de part et d'autre de la limite : deux diffs de 3 900 tokens exacts (via l'aide existante `exactNTokens`), un message système de 400 tokens exacts, `MaxTokens` = 10 000. La somme brute vaut 7 800 ≤ 8 000, donc l'ancien code ne découpe pas ; le prompt rendu vaut environ 8 200 > 8 000, donc le tour 1 échoue.

Le test vérifie deux choses. `len(a.fileGroups) != 2` : le paquet a bien été découpé. Et l'absence de tout avertissement `token_threshold_exceeded` : après découpage, aucun groupe ne se fait rejeter par `checkPromptBudget`. Le second contrôle est celui qui démontre la conséquence utilisateur ; le premier seul pourrait être satisfait par un découpage trop agressif.

Les autres modifications du fichier de test sont mécaniques : `nil` ajouté aux appels de `groupDiffs` et `enforceGroupTokenBudget`, ce qui sélectionne le repli `rawDiffTokens` et laisse ces tests décrire exactement le même comportement qu'avant.

### 6. Limites et effets de bord

- **La guidance de plan n'est pas comptée.** Quand la phase de plan tourne, son résultat est injecté dans le prompt du tour 1 via `{{plan_guidance}}`. `groupPromptTokens` la laisse vide parce qu'elle n'existe pas encore au moment du regroupement. Un groupe peut donc encore, dans ce cas, passer le découpage et échouer au tour 1. C'est écrit dans le commentaire de la fonction.
- **Le découpage reste un tout-ou-rien.** Un groupe trop lourd devient un groupe par fichier, sans étape intermédiaire. Si un seul fichier dépasse à lui seul la limite, il échouera toujours à `checkPromptBudget` ; la PR ne change rien à ce cas.
- **Davantage de groupes, donc davantage d'appels.** Un groupe qui passait avant et qui est maintenant découpé devient N conversations au lieu d'une. Chacune paie le surcoût fixe du prompt, donc le coût total en tokens augmente pour ces cas. Le compromis est explicité dans un commentaire pré-existant de `groupWithoutLLM`. L'alternative — garder le groupe — coûtait zéro token et zéro relecture, puisque tous ses fichiers échouaient.
- **Le rendu du prompt a un coût au moment du regroupement.** `groupPromptTokens` construit les messages et les tokenise une fois par groupe candidat. L'ancienne mesure tokenisait déjà tous les diffs, qui sont la plus grosse part du prompt ; l'ordre de grandeur est le même, mais ce n'est pas gratuit.
- **`enforceGroupTokenBudget` n'est pas appelée sur tous les chemins.** Les replis qui produisent directement des groupes d'un fichier (`toSingleFileGroups`) ne passent pas par elle, ce qui est sans conséquence : ils sont déjà au plus fin.

### 7. Questions probables d'un mainteneur

**« Pourquoi une fonction injectée plutôt que d'appeler `buildMainTaskMessages` depuis `grouping.go` ? »**
Parce que `grouping.go` ne connaît pas l'`Agent`, et que plusieurs appelants — dont les tests et le chemin sans modèle de message — n'ont rien à rendre. L'injection laisse `enforceGroupTokenBudget` testable isolément, ce que font déjà `TestEnforceGroupTokenBudget_NoSplit` et `_Split`, et évite de faire dépendre le fichier de regroupement de tout le rendu de prompt.

**« Le repli `rawDiffTokens` conserve le bug pour tout appelant qui passe `nil`. Est-ce acceptable ? »**
En production, `dispatchSubtasks` est le seul appelant et il passe toujours `a.groupPromptTokens` ; les `nil` sont dans les tests. Le repli existe pour ne pas imposer un modèle de message à un appelant qui n'en a pas et pour garder les tests unitaires du découpage lisibles. Si vous préférez rendre le paramètre obligatoire, cela se fait, au prix de montages de test plus lourds.

**« Rendre le prompt à chaque groupe candidat, n'est-ce pas cher ? »**
C'est un rendu de chaînes plus une tokenisation, une fois par groupe. L'ancienne version tokenisait déjà l'intégralité des diffs, qui représentent la plus grande part du texte. Le supplément, ce sont les règles fusionnées et la liste des fichiers. Le regroupement n'arrive qu'une fois par run, avant les appels au modèle, qui dominent largement.

**« Pourquoi ne pas simplement baisser le seuil de découpage, par exemple à 70 % ? »**
Parce que le surcoût de prompt n'est pas proportionnel à la taille des diffs. Il dépend du nombre de règles système fusionnées et de la longueur de la liste des autres fichiers modifiés. Un seuil fixe serait trop haut pour un gros dépôt avec beaucoup de règles et trop bas pour un petit changement, ce qui découperait inutilement et perdrait le bénéfice du regroupement. Rendre le prompt donne le nombre exact que le contrôle du tour 1 recalculera.

**« La guidance de plan manque. Pourquoi ne pas la compter aussi ? »**
Parce qu'elle n'existe pas encore : le plan est produit dans `executeGroupPlanPhase`, après le regroupement, et son contenu dépend de ce que le modèle répond. On ne peut pas la prévoir. Le commentaire de `groupPromptTokens` le dit, et c'est la seule partie du prompt du tour 1 qui manque. Il resterait possible de réserver une marge pour elle ; je ne l'ai pas fait parce que la taille du plan n'est pas bornée dans le code.

**« Que se passe-t-il si, après découpage, un fichier seul dépasse encore la limite ? »**
Il échoue à `checkPromptBudget` au tour 1, exactement comme avant cette PR. Le découpage par fichier est le grain le plus fin disponible, et cette PR ne touche pas à ce cas. Ce qui change, c'est qu'un fichier qui tenait n'est plus entraîné dans l'échec d'un voisin trop gros.

---

## PR #1477 — Les motifs de fichiers avec accolades imbriquées étaient mal développés

**Branche** : `fix/nested-brace-patterns` · **Fichiers touchés** : `internal/config/rules/system_rules.go`, `internal/config/rules/system_rules_test.go`

### 1. Le problème en une phrase

Le développement maison des accolades fermait chaque groupe à la première `}` rencontrée, ce qui découpait n'importe quel motif imbriqué en motifs invalides, alors que la bibliothèque `doublestar` déjà utilisée pour la comparaison sait parser les accolades imbriquées elle-même.

### 2. Pourquoi ça compte

Un *glob* est un motif de nom de fichier : `*.go`, `**/*.ts`. La notation `{a,b}` désigne des alternatives : `*.{go,py}` veut dire « `*.go` ou `*.py` ». Ces motifs pilotent quatre décisions dans le projet :

- quelle règle de relecture s'applique à un fichier (`SystemRule.Resolve`) ;
- quels fichiers l'utilisateur exclut (`FileFilter.IsUserExcluded`) ;
- quels fichiers l'utilisateur inclut (`FileFilter.IsUserIncluded`) ;
- quelle règle de projet, issue de `.opencodereview/rule.json`, s'applique (`matchProjectRuleEntry`).

Le commentaire du test donne l'exemple exact. Avec `src/{a,{b,c}}/*.go`, l'ancienne fonction produisait `src/a}/*.go`, `src/{b}/*.go` et `src/c}/*.go`. Sur ces trois motifs, seul le deuxième correspondait encore à quelque chose de réel. Traduit en usage : un utilisateur qui écrit ce motif dans son `exclude` croit exclure trois répertoires, et n'en exclut qu'un. Les fichiers de `src/a` et `src/c` sont envoyés au modèle et relus — donc facturés — alors qu'ils devaient être ignorés. Symétriquement, une règle de relecture rattachée à un tel motif ne s'appliquait qu'à un tiers des fichiers visés.

À noter, honnêtement : aucun motif livré dans `internal/config/rules/system_rules.json` n'utilise d'accolades imbriquées. Ils sont tous à un seul niveau (`**/*.{yaml,yml}`, `**/*.{ts,js,tsx,jsx,mjs,cjs}`, etc.). Le défaut touche donc les motifs écrits par les utilisateurs, pas la configuration par défaut.

### 3. Comment le bug se produit

L'ancienne fonction `expandBraces` faisait ceci :

```go
openIdx := strings.IndexByte(s, '{')
closeIdx := strings.IndexByte(s[openIdx:], '}')
```

`IndexByte` renvoie la **première** occurrence. Sur `src/{a,{b,c}}/*.go`, `openIdx` désigne l'accolade ouvrante après `src/`, et `closeIdx` désigne la première `}`, celle qui ferme le groupe *intérieur*. Le code prenait donc pour groupe le texte `a,{b,c`, le découpait sur les virgules en `["a", "{b", "c"]`, et recollait préfixe et suffixe : `src/a` + `}/*.go`, `src/{b` + `}/*.go`, `src/c` + `}/*.go`.

Ensuite, chaque motif ainsi produit passait dans `doublestar.Match`. `src/a}/*.go` cherche littéralement un répertoire nommé `a}`, qui n'existe pas. `src/c}/*.go` idem. Seul `src/{b}/*.go` est, par accident, un motif valide à une alternative, et correspond à `src/b/…`.

Le développement n'était pas non plus récursif : un motif à deux groupes successifs comme `src/{a,b}/*.{go,py}` ne produisait que `src/a/*.{go,py}` et `src/b/*.{go,py}`. Le second groupe restait tel quel — mais dans ce cas précis `doublestar.Match` le rattrapait derrière, ce qui explique pourquoi le défaut est passé inaperçu sur les motifs simples.

Le même bloc était recopié à quatre endroits : `resolveDetail`, `IsUserExcluded`, `IsUserIncluded` et `matchProjectRuleEntry`.

### 4. Ce que fait la correction

`expandBraces` est retirée du code de production. Les quatre sites appellent désormais une seule aide :

```go
func matchGlob(pattern, lowerPath string) bool {
    matched, _ := doublestar.Match(strings.ToLower(pattern), lowerPath)
    return matched
}
```

Le raisonnement : `doublestar.Match` était déjà appelée à la fin de chaque site. Or elle sait traiter les accolades elle-même, y compris imbriquées — sa grammaire documentée inclut `'{' { term } [ ',' { term } … ] '}'`, et elle cherche la fermante *appariée*, pas la première. Développer à la main avant de l'appeler était donc du travail en double, et du travail faux.

Le comportement existant est conservé sur deux points. La casse : le motif et le chemin sont tous deux mis en minuscules, comme avant. Les motifs invalides : une accolade non fermée faisait déjà échouer `doublestar.Match` avec `ErrBadPattern`, donc `matched == false`. L'erreur est ignorée volontairement — le commentaire le dit : « an invalid pattern, such as one with an unclosed brace, matches nothing ».

Alternative écartée : réécrire `expandBraces` correctement, en gérant l'imbrication et la récursion. Le code de cette version correcte existe d'ailleurs — il a été déplacé dans le fichier de test, où il fait une quarantaine de lignes avec suivi de profondeur, gestion de l'échappement par `\` et récursion. Le garder en production aurait signifié maintenir un parseur d'accolades à côté de celui de `doublestar`, pour le même résultat.

Pourquoi le garder dans les tests, alors ? Parce qu'un test d'intégrité existant, `extensions_are_allowlisted`, a besoin d'inspecter **chaque branche** d'un motif pour en extraire l'extension visée et vérifier qu'elle figure dans l'allowlist des types de fichiers. `doublestar` n'expose pas les branches d'un motif, seulement le verdict de correspondance. Le développement reste donc utile comme outil d'inspection, jamais comme mécanisme de correspondance — c'est écrit dans son commentaire.

Enfin, le sous-test `pattern_validity` est simplifié : il valide désormais le motif brut avec `doublestar.ValidatePattern(pr.Pattern)` au lieu de valider chaque branche développée. C'est cohérent avec ce que fait `Resolve` maintenant, et cela prouve au passage que `doublestar` accepte bien les accolades des motifs livrés.

### 5. La preuve

- `TestNestedBracePatterns` est le test central. Il prend un seul motif, `src/{a,{b,c}}/*.go`, et le branche sur les **quatre** consommateurs à la fois : `FileFilter.IsUserExcluded`, `FileFilter.IsUserIncluded`, `SystemRule.Resolve` et `matchProjectRuleEntry`. Il vérifie cinq chemins : `src/a/x.go` et `src/b/x.go` doivent correspondre, `SRC/C/X.GO` aussi (ce qui teste la casse en plus de l'imbrication), `src/d/x.go` et `src/a/x.py` ne doivent pas. Sur `main`, il échoue sur `src/a/x.go` et sur `SRC/C/X.GO` pour les quatre fonctions, avec des messages du type `IsUserExcluded("src/a/x.go") = false, want true`. Les deux cas négatifs sont ce qui empêche de « réparer » le test en rendant la correspondance trop permissive. Le commentaire du test rappelle les trois motifs cassés que l'ancienne version produisait.
- `TestExpandBraces_Nested` vérifie l'aide de test elle-même sur `src/{a,{b,c}}/*.{go,py}`, avec les six résultats attendus dans l'ordre. Il valide à la fois l'imbrication et la récursion sur le suffixe. C'est nécessaire, car un test d'intégrité s'appuie dessus : une aide de test fausse produirait de faux verdicts sur l'allowlist.
- `TestFileFilter_UnclosedBraceMatchesNothing` fixe le comportement des motifs invalides : `*.{go,py` ne doit correspondre à rien, pas même au chemin littéral `*.{go,py`. C'est un test de non-régression sur l'erreur ignorée par `matchGlob`.
- Les tests `TestExpandBraces_NoBraces`, `_SingleGroup`, `_MultipleOptions` et `_UnclosedBrace` sont conservés sans changement, sur l'aide déplacée. Ils prouvent que la nouvelle version récursive donne les mêmes résultats que l'ancienne sur les motifs simples.

### 6. Limites et effets de bord

- **Le comportement change pour les motifs imbriqués, et c'est le but.** Un utilisateur dont l'`exclude` contenait `src/{a,{b,c}}/*.go` excluait en pratique `src/b` seulement. Après cette PR, il exclut aussi `src/a` et `src/c`. S'il s'était habitué au résultat observé, il verra moins de fichiers relus qu'avant. On peut défendre que c'est ce qu'il avait écrit.
- **Aucun motif livré n'est concerné.** Les motifs de `system_rules.json` sont tous à un seul niveau ; le sous-test `pattern_validity` continue de passer. La correction n'affecte donc que les configurations utilisateur.
- **Un motif invalide reste silencieux.** `matchGlob` ignore l'erreur de `doublestar`. Une accolade non fermée ne produit aucun avertissement, le motif ne correspond simplement à rien. C'était déjà le cas ; cette PR le documente et le verrouille par un test, mais ne l'améliore pas. Un motif mal tapé dans un `exclude` reste donc silencieusement sans effet.
- **La casse reste imposée.** Motif et chemin sont mis en minuscules, donc on ne peut pas écrire un motif sensible à la casse. Comportement inchangé.
- **`expandBraces` survit dans les tests.** Un lecteur pressé pourrait croire que le développement maison est toujours en service. Le commentaire de la fonction dit explicitement « Matching does not use it ».

### 7. Questions probables d'un mainteneur

**« Pourquoi garder `expandBraces` du tout, si la correspondance ne l'utilise plus ? »**
Parce que le test d'intégrité `extensions_are_allowlisted` doit lire l'extension visée par chaque branche d'un motif, pour vérifier qu'elle figure bien dans l'allowlist des types de fichiers. Sans cela, une règle peut exister et ne jamais pouvoir s'exécuter, parce que le fichier est écarté sur son extension avant même la résolution de règle. `doublestar` ne donne pas accès aux branches d'un motif, seulement au verdict.

**« La version de `expandBraces` déplacée dans les tests est plus complexe que l'ancienne. Pourquoi ? »**
Parce qu'elle est correcte, et que c'est justement ce que l'ancienne n'était pas. Elle suit la profondeur d'imbrication, ne retient que les virgules du niveau 1, cherche la fermante appariée et se rappelle elle-même sur le reste. Elle gère aussi l'échappement par `\`, comme `doublestar`. Elle a son propre test, `TestExpandBraces_Nested`, plus les quatre tests existants qui prouvent qu'elle ne régresse pas sur les motifs simples.

**« Est-on certain que `doublestar` gère les accolades imbriquées ? »**
Oui, et le test `TestNestedBracePatterns` le vérifie de bout en bout sur les quatre points d'entrée. Dans la version utilisée, `doublestar/v4 v4.10.0`, la correspondance sur `{` cherche la fermante *appariée* et découpe sur les virgules du bon niveau, puis se rappelle récursivement. La grammaire annoncée en tête de son fichier de correspondance inclut explicitement les alternatives.

**« Ignorer l'erreur de `doublestar.Match` cache les motifs invalides. Ne faudrait-il pas avertir ? »**
Ce serait une amélioration réelle, mais c'est un autre changement : il faut un canal d'avertissement dans quatre fonctions qui renvoient aujourd'hui un simple booléen, et décider s'il faut échouer ou continuer. Cette PR conserve le comportement existant, et se contente de le rendre explicite par un commentaire et un test. Je peux ouvrir une PR séparée si vous le souhaitez.

**« Quatre appels remplacés par un seul point de passage : quel est le risque ? »**
Le risque inverse, plutôt : les quatre sites contenaient la même boucle recopiée, et une correction sur un seul les aurait désynchronisés. Les quatre sont maintenant couverts par le même test avec le même motif, donc une divergence future se verrait. Le risque résiduel est qu'un cinquième consommateur oublie d'appeler `matchGlob` ; c'est un risque que le code avait déjà.

**« `pattern_validity` valide maintenant le motif brut. Perd-on une vérification ? »**
On en change la nature. Avant, on validait chaque branche développée, ce qui avait un sens quand c'était la forme réellement passée à `doublestar`. Maintenant c'est le motif brut qui est passé, donc c'est lui qu'il faut valider — sinon on validerait une forme que le code n'utilise plus. Le test a été mis à jour pour coller à ce que fait le code, avec un commentaire qui le dit.

---

## PR #1432 — Les lignes vides empêchaient de placer un commentaire sur un extrait de code

**Branche** : `fix/resolver-blank-lines` · **Fichiers touchés** : `internal/diff/resolver.go`, `internal/diff/relocation_test.go`

### 1. Le problème en une phrase

Pour placer un commentaire, le code compare l'extrait fourni par le modèle aux lignes d'un hunk ; l'extrait avait ses lignes vides retirées, les lignes du hunk non, donc tout extrait couvrant une ligne vide ne correspondait jamais.

### 2. Pourquoi ça compte

Le modèle renvoie ses commentaires avec un extrait de code (`existing_code`) et sans numéro de ligne. C'est `ResolveComment` qui retrouve le numéro, en cherchant cet extrait dans le diff. Sans numéro, le commentaire ne peut pas être ancré à la bonne ligne du fichier.

Or un extrait de code qui contient une ligne vide, c'est le cas le plus banal : deux fonctions consécutives, un bloc séparé par un saut de ligne. L'exemple du test est exactement cela :

```
func old() {}

func older() {}
```

Quand la correspondance sur le hunk échoue, le code se rabat sur `resolveFromFileContent`, qui scanne le contenu du nouveau fichier — et qui, lui, savait déjà sauter les lignes vides (`internal/diff/resolver.go:257-269`). Mais ce repli a deux conditions. Il faut que `d.NewFileContent` soit renseigné. Et surtout, il ne regarde que le **nouveau** fichier : pour du code supprimé, il n'existe nulle part dans la nouvelle version. Le commentaire du test le dit : « there is no file-content fallback for deleted code ».

Un commentaire non placé ne disparaît pas pour autant : `executeToolCall` enchaîne alors sur une re-localisation par le modèle (`ReLocateComment`), un appel supplémentaire, facturé, dont le commentaire de `RelocateAcrossFiles` (`internal/diff/resolver.go:75-96`) décrit lui-même le danger — « il répond avec le token du diff qui ressemble le plus », ce qui écrase la seule preuve pointant vers le vrai code. Un échec évitable de placement coûte donc un appel au modèle et risque un commentaire posé au mauvais endroit.

### 3. Comment le bug se produit

1. `resolveFromHunk` (`internal/diff/resolver.go:151`) prépare l'extrait avec `splitAndNormalize(cm.ExistingCode)`. Cette fonction découpe par ligne, normalise chacune avec `normalizeLine` (`TrimSpace`, retrait d'un `+` ou `-` de tête, `TrimSpace` à nouveau) et **saute les lignes vides** : `if n == "" { continue }` (ligne 299).
2. Elle prépare ensuite le côté du hunk avec `extractSideLines`. L'ancienne version ajoutait *chaque* ligne du hunk au résultat, y compris celles qui se normalisent en chaîne vide :
   ```go
   result = append(result, indexedLine{newLine, normalizeLine(l.Content)})
   ```
   Une ligne vide du diff devenait donc une entrée `{lineNum, ""}` dans la liste.
3. `matchConsecutive` (ligne 225) cherche une suite **consécutive** d'entrées de la liste du hunk dont le contenu est égal, un à un, aux lignes de l'extrait.
4. Le décalage est là. L'extrait normalisé vaut `["func old() {}", "func older() {}"]`, deux éléments côte à côte. La liste du hunk vaut `[…, "func old() {}", "", "func older() {}", …]` : les deux lignes ne sont plus adjacentes, séparées par l'entrée vide. La comparaison élément par élément échoue au deuxième pas. Aucune position ne peut réussir.
5. `resolveFromHunk` renvoie `false` pour le côté nouveau, puis pour le côté ancien, puis renvoie `false`.
6. `ResolveComment` passe à `resolveFromFileContent`. Celui-ci sautait déjà les lignes vides, avec un commentaire qui explique pourquoi — « "Consecutive" here means adjacent non-blank lines ». Mais il ne sert à rien ici : le code supprimé n'est pas dans le nouveau fichier.

Autrement dit, la bonne règle était déjà écrite et appliquée dans le repli ; elle manquait dans le chemin principal.

### 4. Ce que fait la correction

`extractSideLines` saute les lignes qui se normalisent en chaîne vide. Le changement passe par une petite fermeture, pour ne pas répéter le test aux quatre endroits où une ligne est ajoutée :

```go
add := func(lineNum int, content string) {
    if n := normalizeLine(content); n != "" {
        result = append(result, indexedLine{lineNum, n})
    }
}
```

Le point important est que **les compteurs de lignes ne changent pas**. `oldLine++` et `newLine++` restent hors de la fermeture, dans le `switch`, et continuent d'être incrémentés pour toute ligne du hunk, vide ou non. Seule l'entrée dans la liste est omise. Les numéros portés par les entrées restantes restent donc les numéros absolus dans le fichier. C'est ce que dit le commentaire ajouté : « line numbers stay absolute ».

Pourquoi cette approche plutôt qu'une autre ? Deux alternatives existaient.

Insérer les lignes vides dans l'extrait normalisé, pour que les deux côtés aient la même forme. Écartée parce que le modèle ne reproduit pas fidèlement les lignes vides de l'original : il peut en ajouter, en retirer, ou indenter différemment. Exiger une correspondance exacte des lignes vides rendrait le placement plus fragile, pas moins.

Rendre `matchConsecutive` tolérante aux trous. Écartée parce qu'elle est aussi utilisée telle quelle par le repli fichier, qui a déjà résolu le problème en amont, en filtrant sa liste. La correction reprend donc exactement la même stratégie, au même endroit du pipeline : normaliser la liste avant de la comparer. Le commentaire ajouté à `extractSideLines` renvoie explicitement à `splitAndNormalize` pour cette raison.

### 5. La preuve

Un test, `TestResolveComment_DeletedSnippetSpanningBlankLine`.

Il construit un diff dont le hunk `@@ -1,5 +1,2 @@` supprime trois lignes, dont une ligne vide au milieu :

```
 package main
-func old() {}
-
-func older() {}
 func keep() {}
```

Le commentaire porte `ExistingCode: "func old() {}\n\nfunc older() {}"`, c'est-à-dire l'extrait tel qu'un modèle le rendrait, ligne vide comprise. Le test exige d'abord que `ResolveComment` renvoie `true`, puis que les lignes trouvées soient 2 et 4.

Sur `main`, il échoue sur la première assertion : `expected ResolveComment to match across the blank line`. La correspondance côté nouveau échoue (le code supprimé n'y est pas), la correspondance côté ancien échoue à cause de l'entrée vide, et le repli fichier ne démarre pas faute de `NewFileContent`.

Ce test prouve bien le défaut, pour trois raisons. Il choisit du code **supprimé**, donc il ferme la porte au repli fichier : le chemin testé est forcément `resolveFromHunk`, celui qui est corrigé. Il vérifie les numéros de ligne et pas seulement le booléen, ce qui démontre que le saut des lignes vides n'a pas décalé la numérotation — c'est le risque principal de ce genre de correction, et il est vérifié directement : 2 et 4 sont bien les numéros dans l'ancien fichier, l'intervalle contenant la ligne vide 3. Et la plage attendue, 2-4 et non 2-3, montre qu'on rapporte bien la zone complète telle qu'elle existe dans le fichier.

### 6. Limites et effets de bord

- **La correspondance devient plus permissive.** « Consécutif » veut désormais dire « lignes non vides adjacentes ». Un extrait peut donc correspondre à une zone où le fichier a des lignes vides que l'extrait n'avait pas. C'est exactement la permissivité que `resolveFromFileContent` avait déjà ; la PR l'étend au chemin principal plutôt que de l'introduire.
- **La plage rapportée peut inclure des lignes vides.** Dans le test, le commentaire porte sur les lignes 2 à 4, donc il couvre la ligne vide 3, qui n'était pas dans l'extrait au sens strict. C'est le comportement voulu — l'intervalle décrit une zone du fichier, pas une liste de lignes.
- **Un extrait entièrement vide reste non placé.** `splitAndNormalize` renvoie alors une liste vide et `resolveFromHunk` sort tout de suite. Inchangé.
- **Le côté nouveau est essayé avant le côté ancien.** Comme le côté nouveau devient lui aussi plus permissif, un commentaire qui ne correspondait qu'au côté ancien peut désormais correspondre au côté nouveau, et se voir attribuer des numéros du nouveau fichier plutôt que de l'ancien. Le cas est rare (il faut que l'extrait apparaisse des deux côtés à des lignes vides près), mais il est réel et l'ordre d'essai n'a pas été modifié par cette PR.
- **Rien n'est fait pour l'indentation ou les différences de casse.** `normalizeLine` retire déjà les espaces de tête et de fin ; au-delà, un extrait qui diffère du code réel n'est toujours pas placé et part en re-localisation par le modèle.
- **Un seul test ajouté.** Il couvre le cas qui n'avait aucun repli, donc le plus démonstratif. Il ne couvre pas explicitement une ligne vide côté ajouté ; le code emprunte le même chemin, mais ce n'est pas vérifié.

### 7. Questions probables d'un mainteneur

**« Sauter des lignes dans `extractSideLines` ne décale-t-il pas les numéros de ligne ? »**
Non, et c'est le point à vérifier en premier dans le diff. Les incréments `oldLine++` et `newLine++` sont restés dans le `switch`, en dehors de la fermeture `add`. Ils s'appliquent donc à toutes les lignes du hunk, vides comprises. Seul l'ajout à la liste est conditionnel, et chaque entrée conservée porte toujours son numéro absolu. Le test le vérifie en exigeant les lignes 2 et 4, pas 2 et 3.

**« Pourquoi ne pas plutôt conserver les lignes vides dans l'extrait, pour que les deux côtés soient symétriques ? »**
Parce que l'extrait vient du modèle, qui ne reproduit pas fidèlement les lignes vides : il en ajoute, en retire, change l'indentation. Exiger qu'elles correspondent rendrait le placement plus fragile. La direction choisie est l'inverse : retirer le bruit des deux côtés avant de comparer. C'est déjà ce que faisait `splitAndNormalize` pour l'extrait et `resolveFromFileContent` pour le fichier ; le hunk était le seul à ne pas le faire.

**« La correspondance devient plus permissive. Quel est le risque d'un faux positif ? »**
Il existe, mais il est borné : toutes les lignes non vides doivent correspondre exactement, dans l'ordre, après normalisation. Ce qui est relâché, c'est uniquement la présence de lignes vides entre elles. Et ce niveau de permissivité est déjà celui du repli fichier depuis longtemps, avec un commentaire qui l'assume. La PR aligne le chemin principal sur le repli, elle n'ouvre pas une nouvelle porte.

**« Pourquoi un test sur du code supprimé plutôt que sur du code ajouté ? »**
Parce que c'est le seul cas où l'échec est définitif. Pour du code ajouté ou du contexte, `resolveFromFileContent` peut rattraper l'affaire en scannant le nouveau fichier, quand `NewFileContent` est renseigné — et il sautait déjà les lignes vides. Pour du code supprimé, ce repli n'existe pas : le code n'est plus dans le nouveau fichier. Le test vise donc le cas qui échouait vraiment, et il est écrit dans son commentaire.

**« Que se passait-il concrètement avant, quand le placement échouait ? »**
Le commentaire partait en re-localisation par le modèle, `ReLocateComment`, appelée depuis `executeToolCall`. C'est un appel supplémentaire au modèle, donc un coût. Et le commentaire de `RelocateAcrossFiles` décrit le risque de ce chemin : le modèle est sommé de renvoyer un bloc de code et répond avec ce qui ressemble le plus dans le diff qu'on lui montre, ce qui écrase l'extrait d'origine. Le commentaire finit par paraître placé tout en pointant une ligne sans rapport.

**« Le changement touche les deux côtés du hunk. Pourquoi pas seulement le côté ancien, puisque c'est le cas du test ? »**
Parce que le décalage est le même des deux côtés : c'est `splitAndNormalize` qui filtre l'extrait, et il est commun aux deux comparaisons. Ne corriger qu'un côté laisserait le même défaut sur du code ajouté couvrant une ligne vide, avec pour seule différence que le repli fichier peut parfois le masquer. La fermeture `add` est justement là pour appliquer la même règle aux quatre points d'ajout sans les répéter.
