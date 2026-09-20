# Relevé complet des défauts — état au 20 septembre 2026

> Les trois pull requests qui regroupaient plusieurs correctifs ont été découpées : #1474 a donné #1502 et #1503, #1481 a donné #1504, #1431 a donné #1505. Le projet compte donc 24 pull requests, une par changement logique.

45 défauts distincts ont été trouvés, en deux vagues : une première série rencontrée en installant l'outil sous Windows, puis l'audit méthodique des cinq zones du code.

**Résumé des états :**

| État | Nombre |
|---|---|
| Corrigé, dans une PR ouverte | 33 |
| Signalé en privé (sécurité), non corrigé | 6 |
| Question de conception, ouverte en issue | 6 défauts, regroupés en 5 issues |
| **Total** | **45** |

Le détail par zone ci-dessous : première série 9, diff et git 9, modèles d'IA et configuration 8, agent 7, outils et sécurité 4, sessions et sorties 7, plus un défaut trouvé pendant les corrections.

Aucune correction n'est encore fusionnée : les 20 PR attendent une relecture humaine.

---

## Première série — problèmes rencontrés à l'usage (9 défauts)

| # | Défaut | État | Où |
|---|---|---|---|
| 1 | Sous Windows, un délai dépassé sur un script `setup` MCP ne tuait que `cmd.exe` ; l'outil restait bloqué indéfiniment | ✅ Corrigé | PR #1430 |
| 2 | `file_find` renvoyait les noms accentués sous forme échappée (`"caf\303\251.go"`) | ✅ Corrigé | PR #1431 |
| 3 | `code_search` gardait un `\r` en fin de ligne sur les fichiers CRLF | ✅ Corrigé | PR #1505 |
| 4 | `--exclude "src\gen\*"` n'excluait rien sous Windows | ⚠️ Question ouverte | Issue #1463 |
| 5 | Mêmes antislashs dans les motifs de `rule.json` | ⚠️ Question ouverte | Issue #1463 |
| 6 | Un commentaire portant sur du code supprimé contenant une ligne vide n'était jamais placé | ✅ Corrigé | PR #1432 |
| 7 | Durée et nombre de commentaires comptés deux fois dans les métriques | ✅ Corrigé | PR #1433 |
| 8 | `config.json`, qui contient les clés API, écrit sans protection contre une coupure | ✅ Corrigé | PR #1434 |
| 9 | Quatre fonctions dupliquées jamais appelées (162 lignes) | ✅ Corrigé | PR #1435 |

**Sur les défauts 4 et 5 :** ma première correction convertissait tous les antislashs en barres obliques. Le relecteur a montré qu'elle cassait des motifs valides, où l'antislash sert d'échappement (`src/\[generated\]/file.go`). J'ai retiré cette partie de la PR. Le problème est réel mais aucune règle automatique ne peut deviner l'intention dans `src\gen\*` : l'issue #1463 pose la question aux mainteneurs avec trois options.

---

## Zone 1 — Diff et git (9 défauts)

| # | Défaut | État | Où |
|---|---|---|---|
| 1 | Les avertissements de git étaient lus comme des identifiants de commit | ✅ Corrigé | PR #1470 |
| 2 | Un chemin protégé par des guillemets était fusionné avec le fichier précédent | ✅ Corrigé | PR #1472 |
| 3 | Un chemin contenant ` b/` était coupé au mauvais endroit | ✅ Corrigé | PR #1472 |
| 4 | Sous Windows, `*` traversait les `/` dans les motifs `.gitignore` | ✅ Corrigé | PR #1471 |
| 5 | Un motif avec une barre oblique au milieu s'appliquait à toutes les profondeurs | ✅ Corrigé | PR #1471 |
| 6 | Les négations des `.gitignore` imbriqués étaient ignorées | ✅ Corrigé | PR #1471 |
| 7 | Un fichier non suivi dont le nom commence par une espace disparaissait en silence | ✅ Corrigé | PR #1471 |
| 8 | Les modes `--from/--to` et `--commit` utilisent le `.gitignore` du répertoire de travail | ⚠️ Question ouverte | Issue #1487 |
| 9 | Numéro de ligne d'un code supprimé présenté comme celui du nouveau fichier | ⚠️ Question ouverte | Issue #1486 |

---

## Zone 2 — Modèles d'IA et configuration (8 défauts)

| # | Défaut | État | Où |
|---|---|---|---|
| 1 | `ocr llm test` affiche les identifiants contenus dans l'URL | 🔒 Sécurité, signalé en privé | Rapport privé |
| 2 | `ocr config set` affiche en clair les en-têtes secrets | 🔒 Sécurité, signalé en privé | Rapport privé |
| 3 | Une URL Anthropic finissant par `/v1` devenait `/v1/v1/messages` | ✅ Corrigé | PR #1479 |
| 4 | L'en-tête gagnant changeait au hasard selon la casse | ✅ Corrigé | PR #1480 |
| 5 | Les en-têtes réservés écrits dans `config.json` écrasaient la clé API | ✅ Corrigé | PR #1480 |
| 6 | `ocr llm test` ne teste pas le même fichier de configuration que `ocr review` | ⚠️ Question ouverte | Issue #1484 |
| 7 | `config set` supprime les clés de configuration qu'il ne connaît pas | ⚠️ Question ouverte | Issue #1485 |
| 8 | `config set provider ""` créait un fournisseur au nom vide | ✅ Corrigé | PR #1481 |

---

## Zone 3 — Agent et boucle de relecture (7 défauts)

| # | Défaut | État | Où |
|---|---|---|---|
| 1 | `file_read_diff` transmettait au modèle le contenu des fichiers secrets | 🔒 Sécurité, signalé en privé | Rapport privé |
| 2 | La compression de l'historique ne réservait pas la place du prompt fixe | ✅ Corrigé | PR #1473 |
| 3 | Un arrêt au tour 2 marquait « échoués » des fichiers déjà relus au tour 1 | ✅ Corrigé | PR #1474 |
| 4 | Un commentaire sur un fichier renommé était perdu du suivi | ✅ Corrigé | PR #1502 |
| 5 | Le découpage des groupes ignorait le poids du prompt | ✅ Corrigé | PR #1476 |
| 6 | Un commentaire sans chemin recevait la clé du groupe comme chemin | ✅ Corrigé | PR #1503 |
| 7 | `expandBraces` cassait les accolades imbriquées | ✅ Corrigé | PR #1477 |

---

## Zone 4 — Outils et sécurité (4 défauts)

| # | Défaut | État | Où |
|---|---|---|---|
| 1 | `file_read` lit `.env`, les fichiers ignorés et `.git/config`, où réside le jeton CI | 🔒 Sécurité, signalé en privé | Rapport privé |
| 2 | Une jonction Windows contourne le contrôle d'enfermement dans le dépôt | 🔒 Sécurité, signalé en privé | Rapport privé |
| 3 | Aucune limite en octets sur la sortie des outils (50 Mo en un appel) | 🔒 Sécurité, signalé en privé | Rapport privé |
| 4 | Les scripts `setup` MCP contenant des guillemets sont mutilés par `cmd.exe` | ✅ Corrigé | PR #1483 |

---

## Zone 5 — Sessions, scan et sorties (7 défauts)

| # | Défaut | État | Où |
|---|---|---|---|
| 1 | Deux dépôts différents partageaient un même dossier de sessions | ✅ Corrigé | PR #1475 |
| 2 | Les sessions d'un dépôt sur chemin réseau UNC étaient invisibles | ✅ Corrigé | PR #1475 |
| 3 | Un scan repris perdait ses résultats réutilisés si tous les nouveaux fichiers échouaient | ✅ Corrigé | PR #1478 |
| 4 | Les URI SARIF n'étaient pas encodées | ✅ Corrigé | PR #1482 |
| 5 | Le JSON pouvait contenir `"comments": null` au lieu d'une liste vide | ✅ Corrigé | PR #1482 |
| 6 | Le visualiseur coupait les caractères UTF-8 en deux | ✅ Corrigé | PR #1482 |
| 7 | `OCR_ENABLE_TELEMETRY=0` ne pouvait pas désactiver la télémétrie | ✅ Corrigé | PR #1504 |

---

## Un défaut trouvé pendant la correction, hors audit

En corrigeant le défaut 1 de la zone 1, j'ai trouvé le même problème dans `internal/scan/provider.go` : le chemin qui utilise le gestionnaire de processus git mélangeait sortie normale et messages d'erreur, alors que le chemin voisin, sans gestionnaire, prenait explicitement soin de les séparer, commentaire à l'appui. Corrigé dans la même PR #1470.

---

## Ce qui n'est pas corrigé, et pourquoi

**Les 6 failles de sécurité** ne sont pas corrigées par nos soins, volontairement. Les corriger dans une PR publique reviendrait à décrire la faille avant qu'un correctif existe. La règle du projet est de les signaler en privé et de laisser l'équipe publier un correctif et un avis de sécurité. Le rapport est rédigé, il reste à le soumettre.

**Les 5 questions de conception** ne sont pas des défauts d'implémentation mais des choix qui appartiennent aux auteurs : quel fichier de configuration lit `ocr llm test`, faut-il préserver les clés inconnues, faut-il ajouter une notion de côté aux numéros de ligne, quelles règles d'exclusion appliquer en mode commit, et comment accepter les séparateurs Windows. Proposer une correction sans leur accord, c'est prendre une décision d'architecture à leur place.

**Les limites connues des corrections** sont écrites dans chaque PR, section « Limites ». Les trois principales : le filtrage `.gitignore` tient désormais compte des exclusions globales de l'utilisateur ; les sessions déjà enregistrées pour un chemin réseau UNC changent de dossier ; une configuration contenant un en-tête réservé sera désormais refusée avec un message clair.
