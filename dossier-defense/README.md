# Dossier de défense des pull requests

Ce dossier existe pour une raison précise : pouvoir expliquer soi-même chaque changement proposé, comme le règlement du projet l'exige.

| Fichier | Contenu |
|---|---|
| `releve-defauts.md` | Les 45 défauts trouvés, un par ligne, avec leur état : corrigé, signalé en privé, ou question ouverte |
| `preuves-execution.md` | Pour chaque PR, le résultat **observé** quand on relance ses tests sans sa correction |
| `groupe-A.md` | PR #1470, #1471, #1472, #1483, #1430 — git, `.gitignore`, diffs, Windows |
| `groupe-B.md` | PR #1473, #1474, #1476, #1477, #1432 — agent, compression, groupes, règles |
| `groupe-C.md` | PR #1475, #1478, #1482, #1433, #1435 — sessions, scan, sorties, métriques |
| `groupe-D.md` | PR #1479, #1480, #1481, #1431, #1434 — modèles d'IA, configuration, écriture de fichier |

Chaque PR y est traitée en sept points : le problème en une phrase, pourquoi ça compte, comment le bug se produit, ce que fait la correction et pourquoi cette approche, la preuve, les limites, puis les questions probables d'un mainteneur avec leur réponse.

Les notions techniques sont expliquées au fil du texte : sortie standard et sortie d'erreur, hunk, fenêtre de contexte, budget de tokens, SARIF, écriture atomique, liens symboliques.

Rédigé avec Claude Code (modèle Claude Opus 5), puis vérifié en exécutant les tests.
