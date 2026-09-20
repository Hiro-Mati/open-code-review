# Dossier de défense — Groupe D

> **Note de vérification.** Les messages d'échec cités dans ce dossier ont été reconstitués à partir du code des tests. Les résultats réellement observés en exécutant les tests sans leur correction sont consignés dans `preuves-execution.md` : citez ceux-là devant un mainteneur.


Projet : `alibaba/open-code-review` (outil en Go, commande `ocr`).
Cinq pull requests : #1479, #1480, #1481, #1431, #1434.

Ce document sert à défendre chaque changement à l'oral ou par écrit. Il explique
le mécanisme du bug, le raisonnement derrière la correction, et les questions
que peut poser un mainteneur.

---

## Rappels de vocabulaire (à lire une fois)

**Le fichier de configuration.** L'outil lit ses réglages dans
`~/.opencodereview/config.json` (sur Windows, `C:\Users\<nom>\.opencodereview\config.json`).
Ce fichier contient le fournisseur d'IA choisi, le modèle, l'URL du service, et
surtout les clés d'API — des secrets.

**Les variables d'environnement.** Ce sont des réglages posés dans le terminal,
par exemple `OCR_LLM_URL=https://...`. Elles valent pour une session ou pour une
seule commande. Dans ce projet, elles sont prioritaires sur le fichier de
configuration : on peut donc changer un réglage pour un seul lancement sans
toucher au fichier.

**La résolution d'endpoint.** Avant d'appeler une IA, l'outil doit décider où
envoyer la requête et avec quelles informations d'authentification. Cette
décision est prise dans `internal/llm/resolver.go`, par la fonction
`ResolveEndpoint`. Elle essaie quatre sources dans l'ordre (`resolver.go:126-134`) :

1. `OCR config file` — le fichier `config.json` ;
2. `OCR environment` — les variables `OCR_*` ;
3. `Claude Code environment` — les variables de Claude Code ;
4. `Shell rc file` — les variables trouvées dans `.bashrc` / `.zshrc`.

La première source complète gagne. Le résultat est une structure
`ResolvedEndpoint` : URL, jeton, modèle, protocole, en-têtes supplémentaires.

**Les en-têtes HTTP.** Une requête HTTP transporte, en plus du corps du message,
une liste de couples `Nom: valeur` appelés en-têtes. Exemples : `Authorization`
(le jeton d'authentification), `Content-Type` (le format du corps). L'outil
permet d'en ajouter (`extra_headers`), par exemple pour traverser une passerelle
d'entreprise.

**Pourquoi les noms d'en-têtes sont insensibles à la casse.** La norme HTTP
(RFC 9110) dit que `X-Team`, `x-team` et `X-TEAM` désignent le même en-tête. En
HTTP/2 et HTTP/3, les noms sont même toujours transmis en minuscules. La
bibliothèque standard de Go applique cette règle : `http.Header` normalise les
noms avec `http.CanonicalHeaderKey` avant l'envoi. Conséquence pratique : deux
orthographes différentes dans une `map` Go sont deux clés distinctes côté Go,
mais un seul et même en-tête côté réseau.

**La télémétrie.** Ce sont les mesures que l'outil peut envoyer sur son propre
fonctionnement (durée d'une revue, nombre de commentaires, etc.), via le standard
OpenTelemetry. Elle est désactivée par défaut. Une option séparée,
`content_logging`, ajoute le contenu des prompts et des réponses aux journaux :
c'est un réglage sensible pour la vie privée.

**Une écriture atomique.** « Atomique » veut dire : vue de l'extérieur,
l'opération a lieu d'un seul coup, il n'y a pas d'état intermédiaire observable.
Une écriture de fichier ordinaire n'est pas atomique : le système vide d'abord le
fichier, puis y écrit les nouveaux octets. Si la machine s'éteint entre les deux,
il reste un fichier vide ou coupé en deux. La technique standard consiste à
écrire dans un fichier temporaire du même dossier, puis à le renommer par-dessus
la cible : le renommage, lui, est atomique.

**Un lien symbolique (symlink).** C'est un fichier qui ne contient qu'un chemin
vers un autre fichier. Beaucoup de gens gèrent leurs fichiers de configuration
dans un dépôt Git personnel (« dotfiles ») et font de `~/.opencodereview/config.json`
un lien vers un fichier de ce dépôt.

---

## PR #1479 — Normaliser les URL Anthropic finissant par `/v1`

**Branche** : `fix/anthropic-v1-url` · **Fichiers touchés** :
`internal/llm/resolver.go`, `internal/llm/resolver_test.go`

### 1. Le problème en une phrase

Quand l'URL d'un service Anthropic vient d'une variable d'environnement `OCR_*`
ou du bloc `llm` du fichier de configuration, elle n'est pas normalisée, et une
URL finissant par `/v1` devient `/v1/v1/messages` sur le réseau, ce qui renvoie
une erreur 404.

### 2. Pourquoi ça compte

`https://mon-service/v1` est la façon la plus naturelle d'écrire une URL de base :
c'est la forme que publient la plupart des passerelles et des documentations.
L'utilisateur qui l'écrit obtient un 404 à chaque appel, sans indice sur la
cause : le message d'erreur parle d'une ressource introuvable, pas d'une URL mal
construite. Et le comportement est incohérent : la même URL fonctionne si elle
est configurée via un `provider`, mais pas via `OCR_LLM_URL`. Un utilisateur qui
compare les deux chemins ne peut pas comprendre pourquoi.

### 3. Comment le bug se produit

Il existe une fonction dédiée à ce problème, `ensureMessagesSuffix`
(`internal/llm/resolver.go:928`). Elle prend une URL de base et la complète :

- si l'URL finit déjà par `/v1/messages`, elle la laisse telle quelle ;
- si l'URL finit par `/v1`, elle ajoute seulement `/messages` ;
- si l'URL contient `/v1/` au milieu, elle la laisse telle quelle ;
- sinon, elle ajoute `/v1/messages`.

Sur `main`, cette fonction est appelée dans trois des cinq chemins de résolution :
`tryProviderConfig` (`resolver.go:564`), `tryCCEnv` (`resolver.go:713`) et
`tryShellRC` (`resolver.go:799`). Elle n'est appelée **ni** dans `tryOCREnv`
(la stratégie « OCR environment »), **ni** dans `tryLegacyLlmConfig` (le bloc
`llm` historique du fichier de configuration).

Voici le déroulé complet avec `OCR_LLM_URL=https://passerelle.exemple.com/v1` :

1. `ResolveEndpoint` essaie d'abord le fichier de configuration. S'il n'existe
   pas ou n'est pas complet, elle passe à la stratégie « OCR environment ».
2. `tryOCREnv` (`resolver.go:251`) lit `OCR_LLM_URL`, `OCR_LLM_TOKEN`,
   `OCR_LLM_MODEL`. Elle détermine le protocole : par défaut, `OCR_USE_ANTHROPIC`
   vaut vrai, donc le protocole est `anthropic`.
3. Elle retourne un `ResolvedEndpoint` dont l'URL est recopiée telle quelle :
   `https://passerelle.exemple.com/v1`.
4. Le client HTTP est construit par `NewAnthropicClient`
   (`internal/llm/client.go:969-975`). Ce constructeur fait sa propre
   complétion, mais bien plus naïve que `ensureMessagesSuffix` : si l'URL ne
   finit pas par `/v1/messages`, il concatène `/v1/messages`. On obtient
   `https://passerelle.exemple.com/v1/v1/messages`.
5. Ligne suivante (`client.go:976`), le client retire le suffixe `/v1/messages`
   pour donner une URL de base au SDK Anthropic : il reste
   `https://passerelle.exemple.com/v1`. Le SDK, lui, rajoutera `/v1/messages`.
6. Le chemin réellement demandé est donc `/v1/v1/messages`. Aucune passerelle ne
   sert cette route : réponse 404.

Le même enchaînement se produit avec un `config.json` qui utilise le bloc `llm`
historique (`{"llm": {"url": "https://.../v1", ...}}`), traité par
`tryLegacyLlmConfig` (`resolver.go:600` sur `main`, `:604` sur la branche).

### 4. Ce que fait la correction

Deux ajouts, symétriques, de quatre lignes chacun.

Dans `tryOCREnv` (`resolver.go:298-300`), juste avant de construire le résultat :

```go
if protocol == ProtocolAnthropic {
    url = ensureMessagesSuffix(url)
}
```

Dans `tryLegacyLlmConfig` (`resolver.go:691-694`), même chose, en introduisant
une variable locale `url` au lieu d'utiliser directement `cfg.Llm.URL`.

Pourquoi cette approche plutôt qu'une autre :

- **On réutilise la fonction existante** au lieu d'écrire une nouvelle logique.
  Les cinq chemins de résolution se comportent désormais de la même manière,
  ce qui est exactement ce que l'utilisateur attend.
- **On garde la condition sur le protocole.** `/v1/messages` est la route de
  l'API Messages d'Anthropic. Une URL OpenAI ne doit surtout pas la recevoir :
  la condition `protocol == ProtocolAnthropic` reproduit celle qui existe déjà
  dans `tryProviderConfig`.
- **On ne touche pas au client HTTP.** Corriger `NewAnthropicClient` aurait été
  un changement plus large, avec un risque de régression sur les chemins qui
  fonctionnent déjà. La correction reste dans le résolveur, où la normalisation
  est déjà de mise.

### 5. La preuve

Trois tests ajoutés dans `internal/llm/resolver_test.go`.

**`TestResolveEndpoint_AnthropicV1URLNormalized`.** Un tableau de quatre formes
d'URL (`.../v1`, `.../v1/`, sans suffixe, et `.../v1/messages` déjà complète),
chacune testée sur les deux chemins corrigés : variables d'environnement et bloc
`llm`. Huit sous-tests au total. Chacun vérifie que l'URL résolue vaut
`https://api.example.com/v1/messages`.

Sur `main`, les sous-tests avec `.../v1` et `.../v1/` échouent avec :
`URL = "https://api.example.com/v1", want "https://api.example.com/v1/messages"`.

**`TestResolveEndpoint_OpenAIURLNotGivenMessagesSuffix`.** Le garde-fou inverse :
avec `OCR_USE_ANTHROPIC=false`, l'URL `https://api.openai.com/v1` doit rester
inchangée. Ce test protège contre une correction trop large — c'est lui qu'on
montre à un mainteneur qui craint une régression côté OpenAI.

**`TestResolveEndpoint_OCREnvAnthropicV1URLReachesMessagesPath`.** Le test le plus
convaincant. Il monte un vrai serveur HTTP local (`httptest.NewServer`), lui donne
son adresse suivie de `/v1` comme `OCR_LLM_URL`, résout l'endpoint, construit le
vrai client et envoie une vraie requête. Le serveur enregistre le chemin reçu et
répond 404 si ce n'est pas `/v1/messages`.

Sur `main`, ce test échoue et le message affiche le chemin observé :
`CompletionsWithCtx: ... (request path "/v1/v1/messages")`. C'est la démonstration
directe du bug, sans avoir à raisonner sur des chaînes de caractères.

### 6. Limites et effets de bord

- **Champ d'application volontairement étroit.** Seul le protocole Anthropic est
  concerné. Les URL OpenAI et OpenAI Responses ne sont pas touchées.
- **La valeur de `ResolvedEndpoint.URL` change.** Elle contient maintenant le
  chemin complet `/v1/messages` pour ces deux chemins de résolution. Tout ce qui
  affiche cette URL (diagnostic, journal) montrera la forme complète plutôt que
  la forme de base. C'est déjà le cas pour les trois autres chemins.
- **Un cas non traité, hérité de `main`.** Une URL contenant `/v1/` au milieu,
  par exemple `https://passerelle/v1/anthropic`, est laissée intacte par
  `ensureMessagesSuffix`, puis complétée par le client en
  `https://passerelle/v1/anthropic/v1/messages`. Ce comportement existait déjà
  sur les trois chemins déjà normalisés ; cette PR ne le corrige pas, elle se
  contente d'aligner les deux chemins manquants. Il faut le dire franchement si
  la question vient.
- **Un contournement utilisateur peut casser.** Quelqu'un qui aurait découvert le
  bug et écrit volontairement `OCR_LLM_URL=https://passerelle` (sans `/v1`) n'est
  pas affecté : le résultat final est identique. En revanche, si un service
  exposait réellement une route `/v1/v1/messages`, la configuration
  `OCR_LLM_URL=https://passerelle/v1` cesserait de l'atteindre. C'est
  très improbable, mais ce n'est pas impossible.
- **Aucune migration nécessaire.** Aucun fichier de configuration n'est réécrit,
  aucun message d'avertissement n'est ajouté.

### 7. Questions probables d'un mainteneur

**« Pourquoi ne pas corriger `NewAnthropicClient`, qui est la vraie source de la
double concaténation ? »**
Parce que le client est utilisé par tous les chemins de résolution, y compris les
trois qui fonctionnent déjà correctement. Changer sa logique d'URL toucherait à
un code partagé et demanderait de revalider chaque chemin. Ici, on aligne deux
chemins sur le comportement des trois autres, ce qui est un changement local et
vérifiable. Durcir le client peut se faire séparément, si vous le souhaitez.

**« Cette normalisation ne risque-t-elle pas de casser une passerelle qui sert
l'API Messages sur un chemin non standard ? »**
`ensureMessagesSuffix` est justement écrite pour ça : elle ne touche pas une URL
qui finit déjà par `/v1/messages`, ni une URL qui contient `/v1/` en milieu de
chemin. Les seuls cas modifiés sont ceux où l'URL était de toute façon incomplète
et où le client ajoutait déjà un suffixe, mais mal. Le test avec les quatre formes
d'URL couvre ces variantes.

**« Pourquoi la condition sur le protocole, alors que `tryOCREnv` sait déjà que
le protocole est Anthropic à cet endroit ? »**
Elle ne le sait pas toujours. `tryOCREnv` accepte aussi le protocole OpenAI, via
`OCR_LLM_PROTOCOL` ou `OCR_USE_ANTHROPIC=false`. Sans la condition, une URL
OpenAI recevrait un suffixe `/v1/messages` qui n'a aucun sens pour elle. C'est
précisément ce que vérifie `TestResolveEndpoint_OpenAIURLNotGivenMessagesSuffix`.

**« Le test qui monte un serveur HTTP est-il vraiment nécessaire ? Les deux
autres suffisent. »**
Les deux premiers vérifient une chaîne de caractères en sortie du résolveur. Or
le bug se manifestait plus loin, dans le client, qui ajoute encore un suffixe.
Sans le test de bout en bout, on prouve que le résolveur produit la bonne chaîne
sans prouver que la requête part au bon endroit. Le test est local, ne fait aucun
appel réseau externe, et s'exécute en quelques millisecondes.

**« Pourquoi n'y avait-il pas déjà de test pour `tryOCREnv` sur ce point ? »**
Il y avait un test pour le chemin `provider`
(`TestResolveEndpoint_ProviderAnthropicURLHasMessagesSuffix`), mais pas
d'équivalent pour les autres chemins. C'est le schéma classique : la règle a été
posée dans un chemin puis oubliée dans les suivants, et la couverture a suivi la
même asymétrie. Le nouveau test est écrit en tableau pour couvrir les deux
chemins d'un coup, ce qui limite le risque que l'oubli se reproduise.

---

## PR #1480 — Fusion des en-têtes insensible à la casse, et en-têtes réservés dans `config.json`

**Branche** : `fix/extra-headers-merge` · **Fichiers touchés** :
`internal/llm/resolver.go`, `internal/llm/resolver_test.go`

### 1. Le problème en une phrase

Deux défauts sur les en-têtes HTTP supplémentaires : la variable d'environnement
ne remplace pas un en-tête du fichier de configuration si les deux ne sont pas
orthographiés pareil (et c'est alors le hasard qui décide de la valeur envoyée) ;
et un en-tête réservé écrit à la main dans `config.json` échappe à la validation
et remplace silencieusement la clé d'API.

### 2. Pourquoi ça compte

**Le premier défaut produit un comportement non déterministe.** La même
configuration, la même commande, et la valeur envoyée change d'une requête à
l'autre. C'est la pire catégorie de bug pour un utilisateur : le problème
apparaît une fois sur deux, ne se reproduit pas à la demande, et tout diagnostic
devient impossible. Concrètement, un en-tête de routage ou de facturation par
équipe peut partir avec la mauvaise valeur.

**Le second est un problème de sécurité et de confiance.** Un en-tête
`authorization` écrit dans `extra_headers` remplace celui que le SDK construit à
partir de `api_key`. L'utilisateur croit s'authentifier avec la clé qu'il a
configurée ; en réalité c'est une autre valeur qui part, sur chaque requête,
sans le moindre avertissement. Le projet a déjà pris position sur ce point : la
règle existe, elle n'était simplement pas appliquée partout.

### 3. Comment le bug se produit

#### Défaut 1 — la fusion sensible à la casse

Rappel : les noms d'en-têtes HTTP sont insensibles à la casse (voir la section
vocabulaire). Mais en Go, une `map[string]string` distingue `"X-Team"` de
`"x-team"` : ce sont deux clés différentes.

Déroulé :

1. `parseEnvOverrides` (`resolver.go:166`) lit `OCR_LLM_EXTRA_HEADERS` et
   l'analyse avec `ParseExtraHeaders`. L'utilisateur a écrit `x-team=from-env`,
   donc la map contient `{"x-team": "from-env"}`.
2. Le fichier `config.json` déclare, dans le provider actif,
   `"extra_headers": {"X-Team": "from-config", "X-Org-ID": "org-123"}`.
3. `finalizeResolvedEndpoint` (`resolver.go:179` sur `main`) fusionne les deux. Sur `main`,
   la boucle était : pour chaque clé de l'environnement, `ep.ExtraHeaders[key] = value`.
   La clé `"x-team"` est ajoutée ; la clé `"X-Team"` existante n'est pas touchée,
   parce que Go ne voit aucun rapport entre les deux.
4. Résultat : la map contient trois entrées, dont deux pour le même en-tête HTTP.
5. Au moment d'envoyer une requête, le client parcourt cette map
   (`internal/llm/client.go:616` pour OpenAI, `client.go:1292` pour Anthropic) et
   appelle `WithHeader(k, v)` pour chaque entrée.
6. **L'ordre de parcours d'une map en Go est volontairement aléatoire**, et il
   change à chaque parcours. Les deux appels visent le même en-tête ; c'est donc
   le dernier appliqué qui l'emporte, et il diffère d'une requête à l'autre.

Sur vingt requêtes dans le test, la répartition observée était de l'ordre de
5 contre 15 : la valeur du fichier de configuration gagnait environ une fois sur
quatre, alors que l'environnement est censé être prioritaire.

#### Défaut 2 — les en-têtes réservés non validés

Le projet interdit quatre noms d'en-têtes dans `extra_headers`, listés dans
`reservedHeaders` (`resolver.go:844`) : `authorization`, `x-api-key`,
`content-type`, `user-agent`. Les laisser passer produirait des erreurs
d'authentification ou de format sans message clair.

Ce contrôle vit dans `ParseExtraHeaders`, la fonction qui analyse la
**chaîne de caractères** `clé=valeur,clé=valeur`. Elle est appelée dans deux cas :
pour la variable `OCR_LLM_EXTRA_HEADERS` (`resolver.go:171`) et pour
`ocr config set llm.extra_headers` (`cmd/opencodereview/config_cmd.go:529`) ou son
équivalent par provider (`config_cmd.go:672`).

Mais `config.json` n'est pas analysé par `ParseExtraHeaders`. Il est désérialisé
par `encoding/json` directement dans une `map[string]string`
(`resolver.go:334` et `resolver.go:349`). Un utilisateur qui édite le fichier à
la main — ou qui reprend un fichier partagé par un collègue — contourne
entièrement le contrôle.

Effet : `"extra_headers": {"authorization": "Bearer autre-chose"}` est accepté,
transmis au client, appliqué à chaque requête, et écrase l'en-tête
d'authentification construit à partir de `api_key`.

### 4. Ce que fait la correction

**Une fonction `mergeExtraHeaders`** (`resolver.go:200`), appelée par
`finalizeResolvedEndpoint` à la place de la boucle. Elle procède en trois temps :

1. Construire l'ensemble des noms présents côté environnement, sous forme
   canonique (`http.CanonicalHeaderKey`, qui transforme `x-team` en `X-Team`).
2. Recopier les entrées du fichier de configuration **sauf** celles dont le nom
   canonique figure dans cet ensemble.
3. Ajouter toutes les entrées de l'environnement.

Le résultat est une **nouvelle map**. Pourquoi cette approche :

- **`http.CanonicalHeaderKey` plutôt qu'un `strings.ToLower` maison.** C'est la
  fonction de la bibliothèque standard, celle-là même qu'utilise le client HTTP
  de Go pour normaliser les en-têtes. On applique donc exactement la règle que le
  réseau appliquera, sans réimplémenter la norme.
- **Une nouvelle map plutôt qu'une modification sur place.** L'ancien code
  écrivait dans la map issue du fichier de configuration. C'est un effet de bord
  sur une donnée qui appartient à l'appelant. Retourner une copie supprime ce
  partage implicite.
- **On ne garde pas les deux orthographes en espérant que le client trie.** Le
  client applique les en-têtes un par un ; le tri doit avoir lieu là où l'on
  connaît la priorité, c'est-à-dire dans le résolveur.

**Une validation des en-têtes réservés côté fichier de configuration.** Le
contrôle a été extrait de `ParseExtraHeaders` dans une fonction
`checkReservedHeader` (`resolver.go:878`), pour éviter de dupliquer le message
d'erreur. Une fonction `validateExtraHeaders` (`resolver.go:890`) l'applique à
une map entière, et est appelée à deux endroits :

- `tryProviderConfig` (`resolver.go:570`), qui couvre les providers prédéfinis et
  les providers personnalisés ;
- `tryLegacyLlmConfig` (`resolver.go:696`), qui couvre le bloc `llm` historique.

`validateExtraHeaders` **trie les noms avant de les parcourir**. Sans cela, avec
deux en-têtes réservés dans le même fichier, le nom cité dans le message d'erreur
changerait d'une exécution à l'autre, à cause du même parcours aléatoire de map.
Un message d'erreur doit être reproductible.

Le message reste celui qui existait déjà :
`extra header "authorization" conflicts with a reserved header; use the dedicated
config field instead`.

### 5. La preuve

Trois tests ajoutés dans `internal/llm/resolver_test.go`.

**`TestResolveEndpoint_EnvExtraHeaderOverridesConfigCaseInsensitively`.**
Configuration avec `X-Team: from-config` et `X-Org-ID: org-123`, environnement
avec `x-team=from-env`. Le test attend exactement
`{"x-team": "from-env", "X-Org-ID": "org-123"}`.
Sur `main`, la map contient une troisième entrée et le test affiche les deux maps
côte à côte, ce qui rend la duplication visible d'un coup d'œil.

**`TestResolveEndpoint_EnvExtraHeaderOverrideOnTheWire`.** Le test qui prouve le
caractère aléatoire. Il monte un serveur HTTP local qui compte, pour chaque
requête, la valeur reçue dans l'en-tête `X-Team`. Il envoie **vingt** requêtes et
exige que les vingt portent `from-env`.

Le nombre vingt n'est pas décoratif : avec une seule requête, le test passerait
sur `main` une fois sur deux environ. Vingt requêtes rendent l'échec quasi
certain sur `main`, et le message affiche la répartition observée
(`X-Team values on the wire = map[from-config:5 from-env:15], want from-env on
all 20 requests`), ce qui documente le bug mieux qu'une phrase.

**`TestResolveEndpoint_ConfigFileExtraHeadersReservedRejected`.** Trois
sous-cas : provider personnalisé avec `authorization`, provider prédéfini avec
`X-Api-Key`, bloc `llm` historique avec `Content-Type`. Chacun doit produire une
erreur contenant le mot `reserved`. Les trois sous-cas couvrent les trois
chemins de lecture du fichier, et les orthographes variées (`authorization`,
`X-Api-Key`, `Content-Type`) montrent que le contrôle est bien insensible à la
casse.

Sur `main`, les trois échouent avec
`expected error for reserved extra header in config file`.

### 6. Limites et effets de bord

- **Changement visible pour les utilisateurs : une configuration contenant un
  en-tête réservé sera désormais refusée.** Un `config.json` avec
  `"extra_headers": {"authorization": "..."}` fonctionnait (mal) hier ;
  aujourd'hui, la résolution de l'endpoint échoue avec une erreur explicite.
  C'est le but — l'ancien comportement remplaçait la clé d'API en silence — mais
  c'est bien un changement de comportement à annoncer.
- **L'erreur survient au lancement d'une revue, pas au moment de l'édition.** La
  validation est dans le résolveur. Quelqu'un qui édite `config.json` ne voit
  rien tant qu'il ne relance pas l'outil. Ajouter le même contrôle à la lecture
  du fichier par les commandes `ocr config` serait un complément raisonnable,
  hors périmètre ici.
- **Le chemin interactif n'est pas couvert par cette PR.** L'interface TUI de
  configuration recopie les en-têtes (`cmd/opencodereview/provider_tui.go:1309-1312`)
  sans appliquer le contrôle. La validation du résolveur les rattrape au
  lancement suivant, mais l'utilisateur ne l'apprend pas immédiatement.
- **La déduplication ne joue qu'entre les deux sources.** Si `config.json`
  contient lui-même `X-Team` et `x-team`, les deux sont conservés et le
  non-déterminisme subsiste pour ce cas précis. C'est un fichier écrit à la main
  avec deux orthographes du même en-tête, donc un cas rare, mais la PR ne le
  traite pas.
- **L'orthographe conservée est celle de la source gagnante.** La map résolue peut
  donc mélanger `x-team` (venu de l'environnement) et `X-Org-ID` (venu du
  fichier). C'est sans conséquence : le client HTTP normalise les noms avant
  l'envoi, et le test de bout en bout le confirme.
- **Une allocation supplémentaire par résolution**, uniquement quand
  `OCR_LLM_EXTRA_HEADERS` est définie. Négligeable : la résolution a lieu une
  fois par exécution.
- **`checkReservedHeader` applique désormais un `TrimSpace` sur le nom.** Un
  en-tête écrit `" Authorization "` dans une chaîne `OCR_LLM_EXTRA_HEADERS` est
  maintenant rejeté alors qu'il passait avant. C'est un durcissement mineur et
  cohérent, mais c'est un changement réel.

### 7. Questions probables d'un mainteneur

**« Pourquoi refuser la configuration au lieu d'émettre un avertissement et
d'ignorer l'en-tête réservé ? »**
Parce que la règle existe déjà et qu'elle est déjà bloquante ailleurs :
`OCR_LLM_EXTRA_HEADERS` et `ocr config set` renvoient une erreur. Faire autrement
pour le fichier créerait une troisième règle. Et un avertissement sur un problème
d'authentification passe facilement inaperçu dans la sortie d'une revue. Cela
dit, si vous préférez un avertissement pour préserver les configurations
existantes, le changement est localisé dans `validateExtraHeaders`.

**« Ce refus va casser des configurations qui tournaient. Avez-vous prévu une
migration ? »**
Non, et c'est un point à trancher avec vous. Mon raisonnement : une configuration
concernée n'envoyait pas la clé d'API attendue — elle « tournait » avec des
informations d'authentification que l'utilisateur ne croyait pas utiliser.
L'erreur est explicite et nomme l'en-tête fautif, donc la correction côté
utilisateur prend quelques secondes. Si vous jugez cela trop brutal pour une
version mineure, on peut le passer en avertissement puis en erreur plus tard.

**« Vingt requêtes dans un test unitaire, n'est-ce pas un test lent et fragile ? »**
Le serveur est local (`httptest`), il n'y a aucun accès réseau externe et le test
s'exécute en quelques dizaines de millisecondes. Le nombre vingt sert à rendre
l'échec fiable sur `main` : avec une seule requête, le test passerait environ une
fois sur deux, ce qui en ferait un test inutile. Après la correction, le test est
strictement déterministe : il n'y a plus qu'une valeur possible.

**« `http.CanonicalHeaderKey` gère-t-elle correctement des noms exotiques,
par exemple avec des caractères non ASCII ? »**
Elle retourne le nom inchangé quand il ne correspond pas à la forme attendue d'un
nom d'en-tête. Dans ce cas, la comparaison retombe sur une égalité exacte, c'est-à-dire
exactement le comportement d'avant la correction. On ne perd donc rien pour ces
noms, et on gagne le bon comportement pour tous les noms d'en-têtes valides, qui
sont en ASCII par définition.

**« Pourquoi trier les noms dans `validateExtraHeaders` ? »**
Pour que le message d'erreur soit reproductible. Si un fichier contient deux
en-têtes réservés, un parcours de map non trié citerait tantôt l'un, tantôt
l'autre. Un utilisateur qui relance la commande et voit un message différent
perd confiance dans l'outil, et un rapport de bug devient impossible à comparer.

**« Le résultat de la fusion est une map avec des orthographes mélangées.
Est-ce acceptable ? »**
Oui, parce que la normalisation finale se fait de toute façon dans la couche
HTTP de Go, au moment de construire la requête. Normaliser aussi les clés dans le
résolveur changerait ce que l'utilisateur voit s'il inspecte l'endpoint résolu,
sans rien améliorer sur le réseau. Le test de bout en bout vérifie ce qui compte :
la valeur réellement reçue par le serveur.

---

## PR #1481 — Refuser un nom de provider vide, et laisser l'environnement désactiver la télémétrie

**Branche** : `fix/config-validation` · **Fichiers touchés** :
`cmd/opencodereview/config_cmd.go`, `cmd/opencodereview/config_cmd_test.go`,
`internal/telemetry/config.go`, `internal/telemetry/config_test.go`,
`pages/src/content/docs/{en,ja,ko,ru,zh}/telemetry.md`

### 1. Le problème en une phrase

Deux défauts de validation de configuration : `ocr config set provider ""`
enregistre un provider inutilisable dont le nom est vide, et
`OCR_ENABLE_TELEMETRY` ne sait qu'activer la télémétrie, jamais la désactiver,
alors que la documentation annonce que l'environnement est prioritaire.

### 2. Pourquoi ça compte

**Pour le provider vide** : la commande réussit, l'utilisateur croit avoir fait
quelque chose, et le fichier de configuration contient une entrée qu'aucun chemin
du code ne peut sélectionner. Pire, la commande efface au passage le modèle
configuré. L'utilisateur se retrouve avec une configuration abîmée sans le moindre
message.

**Pour la télémétrie** : c'est un problème de contrôle et de vie privée. Si un
`config.json` d'équipe active la télémétrie, ou pire `content_logging` — qui fait
figurer le contenu des prompts et des réponses dans les journaux —, un utilisateur
ne peut pas la couper pour un lancement. La documentation lui dit pourtant que
l'environnement est prioritaire, et il est naturel d'essayer
`OCR_ENABLE_TELEMETRY=0`. Cette commande ne fait rien, silencieusement. Un
utilisateur peut donc croire à tort qu'il a désactivé l'envoi de données.

### 3. Comment le bug se produit

#### Défaut 1 — le nom de provider vide

`setConfigValue` (`cmd/opencodereview/config_cmd.go:446`) traite la clé
`provider`. Sur `main`, avec `value = ""` :

1. `cfg.Provider != value` est vrai si un provider était sélectionné, donc
   `cfg.Model = ""` : **le modèle est effacé**.
2. `cfg.Provider = ""`.
3. `llm.LookupProvider("")` ne trouve rien : la chaîne vide n'est pas un provider
   prédéfini.
4. On tombe donc dans la branche « provider personnalisé » et on écrit
   `cfg.CustomProviders[""] = ProviderEntry{}`.
5. La configuration est enregistrée. Le fichier contient
   `"custom_providers": {"": {}}`.

Ensuite, à la résolution : `tryOCRConfig` (`resolver.go:347`) teste
`if cfg.Provider != ""`. Comme le provider est vide, la condition est fausse et
le code part sur `tryLegacyLlmConfig`, le bloc `llm` historique. L'entrée
`custom_providers[""]` n'est donc **jamais** lue, par aucun chemin.

Bilan : une commande qui réussit, qui casse la configuration existante (le modèle)
et qui crée une entrée morte.

À noter : la commande pour effacer un provider existe déjà, c'est
`ocr config unset provider` (`config_cmd.go:155-157`, qui appelle
`unsetActiveProvider`). L'utilisateur qui écrit `config set provider ""` cherche
simplement cette commande sans la connaître.

#### Défaut 2 — la télémétrie qu'on ne peut pas éteindre

La résolution de la configuration de télémétrie est en trois couches
(`internal/telemetry/config.go`, fonction `ResolveConfig`) :

1. `DefaultConfig()` — tout est désactivé ;
2. `LoadFromJSON` — la section `telemetry` de `config.json` ;
3. `resolveEnv` — les variables d'environnement.

Le commentaire du code dit explicitement « Environment takes highest priority »,
et la documentation le répète.

Sur `main`, `resolveEnv` était écrite ainsi :

```go
if os.Getenv("OCR_ENABLE_TELEMETRY") == "1" {
    cfg.Enabled = true
}
```

Cette forme ne peut que mettre la valeur à vrai. Elle n'a **aucun** effet quand la
variable vaut `0` : la condition est fausse, et la ligne est sautée. La valeur
issue de `config.json` reste donc en place.

Concrètement, avec `{"telemetry": {"enabled": true, "content_logging": true}}` :
`OCR_ENABLE_TELEMETRY=0 ocr review` envoie quand même la télémétrie, et
`OCR_CONTENT_LOGGING=0` n'empêche pas le contenu des prompts d'entrer dans les
journaux. La troisième couche n'est prioritaire que dans un sens.

### 4. Ce que fait la correction

#### Pour le provider

Cinq lignes au début du cas `provider` (`config_cmd.go:459-464`) :

```go
if strings.TrimSpace(value) == "" {
    return fmt.Errorf("provider name must not be empty; use 'ocr config unset provider' to clear it")
}
```

Pourquoi cette approche :

- **On refuse avant toute modification.** Le `return` précède la ligne qui efface
  le modèle. La configuration ressort donc strictement inchangée.
- **On refuse aussi les noms composés uniquement d'espaces** (`" "`, tabulation),
  grâce à `TrimSpace`. Ils poseraient exactement le même problème : aucun chemin
  de résolution ne les sélectionne.
- **Le message indique la commande correcte.** L'utilisateur qui écrit
  `config set provider ""` veut presque toujours effacer le provider ; on le
  renvoie vers `ocr config unset provider` plutôt que de le laisser chercher.

#### Pour la télémétrie

Une petite fonction `envFlag` (`internal/telemetry/config.go:48`) qui retourne
deux valeurs : la valeur booléenne, et un indicateur disant si la variable
portait bien une valeur reconnue.

```go
func envFlag(name string) (value, set bool) {
    switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
    case "1":
        return true, true
    case "0", "false", "no", "off":
        return false, true
    }
    return false, false
}
```

Les deux appels deviennent `if v, ok := envFlag("OCR_ENABLE_TELEMETRY"); ok { cfg.Enabled = v }`.

Pourquoi cette approche :

- **Le second retour est indispensable.** Sans lui, on ne peut pas distinguer
  « la variable dit faux » de « la variable n'est pas définie ». Le premier cas
  doit écraser le fichier ; le second doit le laisser intact.
- **Le côté « activation » est inchangé.** Seul `1` active, exactement comme
  avant. Aucune configuration qui fonctionnait ne change de comportement.
- **Une valeur non reconnue reste sans effet.** `OCR_ENABLE_TELEMETRY=maybe` ne
  fait rien, comme sur `main`. On n'introduit pas une nouvelle façon d'échouer.
- **`TrimSpace` et `ToLower`** absorbent les cas courants (`FALSE`, `" 0 "`), qui
  arrivent souvent quand la variable est posée par un script.

La documentation `telemetry.md` est mise à jour dans les cinq langues du site
(anglais, japonais, coréen, russe, chinois), avec une phrase symétrique de celle
qui existait déjà pour l'activation.

### 5. La preuve

**`TestSetConfigValueProviderRejectsEmptyName`**
(`cmd/opencodereview/config_cmd_test.go`). Trois valeurs testées : `""`, `"   "`,
`"\t"`. Pour chacune, on part d'une configuration réaliste
(`Provider: "anthropic"`, `Model: "claude-opus-4-6"`) et on vérifie trois choses :
une erreur est retournée ; le provider et le modèle sont inchangés ; aucune entrée
`CustomProviders` n'a été créée.

La deuxième vérification est celle qui compte le plus : elle documente l'effet de
bord caché du bug, l'effacement du modèle.

Sur `main`, le test échoue sur la première assertion :
`expected error for empty provider name, got nil`.

**Tests de télémétrie** (`internal/telemetry/config_test.go`), ajoutés dans
`TestResolveConfig`. Deux boucles :

- Six valeurs « fausses » — `"0"`, `"false"`, `"FALSE"`, `"no"`, `"off"`, `" 0 "` —
  avec un `config.json` qui active `enabled` et `content_logging`. Le test exige
  que les deux ressortent désactivés. Les variantes de casse et d'espaces
  vérifient la normalisation.
- Deux valeurs non reconnues — `""` et `"maybe"` — avec le même fichier. Le test
  exige cette fois que les deux restent **activés**. C'est le garde-fou contre
  une correction trop large qui interpréterait « valeur inconnue » comme « faux »
  et couperait la télémétrie de quelqu'un sans le vouloir.

Sur `main`, les six premiers sous-tests échouent avec, par exemple :
`OCR_ENABLE_TELEMETRY="0" should disable telemetry enabled in json`.

### 6. Limites et effets de bord

- **Changement visible : `ocr config set provider ""` échoue maintenant.** Un
  script qui utilisait cette commande pour effacer le provider doit passer à
  `ocr config unset provider`. Le message d'erreur le dit.
- **Changement visible : `OCR_ENABLE_TELEMETRY=0` désactive réellement la
  télémétrie.** Si quelqu'un avait cette variable posée dans son environnement
  depuis longtemps, sans effet, et comptait sur `config.json` pour activer la
  télémétrie, celle-ci s'arrête. C'est le comportement documenté, mais c'est un
  changement réel.
- **L'activation et la désactivation ne sont pas symétriques.** `1` active, mais
  `true`, `yes` et `on` n'activent pas. C'est volontaire : ces valeurs n'avaient
  aucun effet sur `main`, et je n'ai pas voulu élargir la surface d'activation
  dans une correction de bug. C'est néanmoins une incohérence assumée, que je
  peux lever si vous le souhaitez.
- **Le nom du provider n'est pas nettoyé, seulement contrôlé.** `"  openai  "`
  est toujours accepté et enregistré avec ses espaces. Ajouter un `TrimSpace` sur
  la valeur stockée changerait le comportement pour des noms valides, donc je
  l'ai laissé de côté.
- **Les autres clés de `config set` ne reçoivent pas ce contrôle.** Cette PR ne
  traite que `provider`, la seule pour laquelle une valeur vide produit une
  entrée morte dans le fichier.
- **Les variables `OTEL_*` gardent leur ancienne règle** (« une valeur non vide
  écrase »). Ce sont des chaînes, pas des interrupteurs ; la notion de « valeur
  fausse » ne s'y applique pas.
- **La PR regroupe deux corrections sans lien technique.** Elles partagent le
  thème de la validation de configuration, mais un mainteneur peut légitimement
  demander de les séparer ; c'est facile, les fichiers sont disjoints.

### 7. Questions probables d'un mainteneur

**« Pourquoi deux corrections sans rapport dans la même PR ? »**
Elles viennent du même passage d'audit sur la validation de configuration et
touchent des fichiers disjoints, donc la relecture reste simple. Si vous préférez
deux PR, je les sépare sans difficulté : la partie provider est dans
`cmd/opencodereview/`, la partie télémétrie dans `internal/telemetry/` plus la
documentation.

**« Pourquoi n'acceptez-vous pas `true`, `yes` ou `on` pour activer ? »**
Pour ne pas changer le comportement existant. Sur `main`, seul `1` activait, et
`true` ne faisait rien. Si je l'acceptais, quelqu'un qui a
`OCR_ENABLE_TELEMETRY=true` posée depuis des mois sans effet verrait soudain la
télémétrie s'allumer. Une correction de bug ne devrait pas activer un envoi de
données que personne n'a demandé. Rendre la chose symétrique est une bonne idée,
mais c'est un changement à annoncer séparément.

**« Une valeur non reconnue devrait-elle produire une erreur plutôt qu'être
ignorée ? »**
Ce serait défendable, mais c'est un autre changement. Aujourd'hui, la couche
télémétrie ne remonte aucune erreur : `ResolveConfig` retourne une `Config`, et
même un JSON invalide est ignoré en silence. Introduire une erreur ici demanderait
de changer cette signature et de décider où l'afficher. J'ai gardé le périmètre
sur le défaut signalé.

**« Le refus du nom vide ne risque-t-il pas de casser des scripts ? »**
C'est possible, pour un script qui utilisait `config set provider ""` comme
moyen d'effacer le provider. Mais ce script produisait déjà une configuration
abîmée : il effaçait le modèle et laissait une entrée inutilisable dans le
fichier. Le message d'erreur nomme la commande de remplacement, donc la
correction côté utilisateur est d'une ligne.

**« Pourquoi refuser plutôt que de traiter la chaîne vide comme un `unset` ? »**
Parce qu'une commande ne devrait pas faire silencieusement autre chose que ce
qu'elle annonce. `config set` définit une valeur ; `config unset` en supprime une.
Réutiliser `set` pour supprimer rendrait le comportement dépendant d'une
convention implicite. Et comme `config unset provider` existe déjà, il n'y a rien
à inventer.

**« Avez-vous vérifié que `content_logging` suit la même règle ? »**
Oui, les deux interrupteurs passent par la même fonction `envFlag` et sont
couverts par les mêmes sous-tests, qui vérifient simultanément `cfg.Enabled` et
`cfg.ContentLog`. C'était important à traiter ensemble : `content_logging` est le
plus sensible des deux, puisqu'il fait entrer le contenu des prompts dans les
journaux.

---

## PR #1431 — Deux corrections sur la lecture de la sortie de git

**Branche** : `fix/windows-path-handling` · **Fichiers touchés** :
`internal/tool/file_find.go`, `internal/tool/file_find_test.go`,
`internal/tool/code_search.go`, `internal/tool/code_search_test.go`

### 1. Le problème en une phrase

Deux outils de l'agent lisent mal la sortie de git : `file_find` renvoie les noms
de fichiers non ASCII sous leur forme échappée et entre guillemets, et
`code_search` renvoie les lignes des fichiers en CRLF avec un retour chariot
parasite à la fin.

### 2. Pourquoi ça compte

Ces deux outils sont appelés par le modèle pendant une revue. Ce qu'ils renvoient
devient le contexte à partir duquel le modèle raisonne.

Pour `file_find`, la conséquence est bloquante : le modèle reçoit
`"caf\303\251.go"` et rappelle l'outil de lecture avec ce nom-là, qui n'existe
pas sur le disque. Le fichier devient inaccessible à l'agent. Tout projet dont
les noms de fichiers comportent des accents, des caractères chinois, japonais,
coréens ou cyrilliques est concerné — ce qui, pour un projet hébergé par Alibaba
avec une documentation en cinq langues, n'est pas un cas marginal.

Pour `code_search`, la conséquence est plus discrète : un `\r` en fin de chaque
ligne extraite. Cela pollue le contexte, peut fausser une comparaison de texte,
et fait apparaître des caractères parasites dans les extraits cités. Les dépôts
avec des fichiers en CRLF sont courants, en particulier sous Windows.

### 3. Comment le bug se produit

#### `file_find` et `core.quotepath`

Git a une option de configuration nommée `core.quotepath`, **activée par défaut**.
Quand elle est active, tout chemin contenant un octet hors ASCII est affiché entre
guillemets, avec les octets non ASCII remplacés par des échappements octaux de
style C. Le fichier `café.go` est encodé en UTF-8 par les octets
`63 61 66 C3 A9 2E 67 6F`, et git l'affiche donc `"caf\303\251.go"` (`\303\251`
étant `C3 A9` en octal).

Cette option existe pour que la sortie de git reste lisible sur un terminal qui
ne gère pas l'UTF-8. Pour un programme qui analyse cette sortie, c'est une nuisance.

Le reste du projet le sait et passe systématiquement `-c core.quotepath=false` :
`internal/diff/git.go` (six appels), `internal/scan/provider.go:246`,
`internal/tool/filereader.go:122` et `:208`, `internal/config/rules/sniffer.go:124`,
et même `internal/tool/code_search.go:62` avec un commentaire qui explique
pourquoi. `internal/tool/file_find.go` était le seul appel git à ne pas le faire
(`listGitFiles`, `file_find.go:107-113`), pour ses deux variantes :
`git ls-files` en mode espace de travail, `git ls-tree` quand une revue porte sur
une référence précise.

Déroulé complet :

1. L'agent appelle `file_find` avec `query_name: "caf"`.
2. `listGitFiles` lance `git ls-files --cached --others --exclude-standard`.
3. Git, avec `core.quotepath` actif par défaut, écrit `"caf\303\251.go"`.
4. L'outil recopie cette ligne telle quelle dans son résultat.
5. Le modèle appelle ensuite l'outil de lecture avec ce nom. Le chemin ne
   correspond à aucun fichier : la lecture échoue.

Le préfixe `-c clé=valeur` demande à git d'utiliser cette valeur **pour cette
commande uniquement** ; la configuration de l'utilisateur n'est pas modifiée.

#### `code_search` et les fins de ligne CRLF

Sous Windows, une fin de ligne s'écrit traditionnellement avec deux octets :
retour chariot (CR, `\r`) puis saut de ligne (LF, `\n`). Sous Unix, un seul :
LF. Un dépôt peut contenir des fichiers stockés avec des fins de ligne CRLF.

`git grep` produit des lignes de la forme `fichier:numéro:contenu`, séparées par
des LF. Quand le fichier est en CRLF, le CR fait partie du contenu tel que git le
restitue : la ligne reçue se termine par `...}\r`.

Dans `gitGrep` (`internal/tool/code_search.go`), le contenu est extrait avec
`strings.SplitN(line, ":", splitN)` puis stocké tel quel :
`m := match{lineNum: ln, content: parts[offset+2]}`. Le `\r` est conservé et
ressort dans le résultat de l'outil.

### 4. Ce que fait la correction

**`file_find.go:110` et `:112`** — ajout de `-c core.quotepath=false` en tête des
deux listes d'arguments, pour `ls-tree` comme pour `ls-files`.

**`code_search.go:225`** — le contenu est passé par `strings.TrimSuffix(..., "\r")`,
avec un commentaire d'une ligne.

Pourquoi ces approches :

- **Pour `file_find`, on aligne sur ce que fait déjà tout le reste du projet.**
  On n'invente pas une règle : on applique celle qui est en vigueur partout
  ailleurs, avec exactement la même syntaxe. Un relecteur peut le vérifier par un
  simple `grep core.quotepath`.
- **On ne décode pas les échappements côté Go.** Écrire un décodeur de la forme
  `"caf\303\251.go"` serait possible, mais ce serait réimplémenter une syntaxe de
  git, avec ses cas particuliers (guillemets, antislashs, `\t`, `\n`). Demander à
  git de ne pas échapper est plus simple et plus sûr.
- **On ne touche pas à la configuration de l'utilisateur.** Le préfixe `-c` est
  limité à l'invocation.
- **Pour `code_search`, `TrimSuffix` plutôt qu'un remplacement global.** Un `\r`
  au milieu d'une ligne est un caractère réel du fichier ; le supprimer serait
  falsifier le contenu. Seul le `\r` de fin de ligne est un artefact de la
  représentation CRLF.

### 5. La preuve

**`TestFileFind_NonASCIIPath`** (`internal/tool/file_find_test.go`). Le test crée
un dépôt de travail, y écrit un fichier `café.go`, puis exécute
`git config core.quotepath true` **dans ce dépôt**. Ce détail est important :
sans lui, le test passerait à tort sur la machine d'un développeur qui aurait
désactivé l'option globalement. On force donc le comportement par défaut de git
pour que le test soit valable partout.

Le fichier est ensuite validé dans un commit, et l'outil est exécuté deux fois :
une fois en mode espace de travail (`Ref: ""`, chemin `git ls-files`), une fois
sur le commit (`Ref: <sha>`, chemin `git ls-tree`). Les deux chemins étaient
touchés par le bug, les deux sont vérifiés. L'assertion exige que la sortie
contienne `café.go` littéralement et ne contienne pas `\303`.

Sur `main`, les deux sous-cas échouent avec
`ref "": expected verbatim "café.go", got: ... "caf\303\251.go"`, ce qui montre
la forme échappée dans le message même.

**`TestGitGrep_CRLFFileHasNoTrailingCR`** (`internal/tool/code_search_test.go`).
Le test écrit un fichier dont toutes les fins de ligne sont CRLF, lance `gitGrep`,
et vérifie deux choses : la ligne attendue `3|func Crlf() {}\n` est bien présente
(donc le contenu n'a pas été abîmé), et le résultat entier ne contient aucun `\r`.

La seconde assertion est plus large que la première : elle attrape aussi un `\r`
qui aurait survécu ailleurs dans la mise en forme. Sur `main`, le test échoue en
affichant la chaîne entre guillemets, où le `\r` est visible :
`expected CR-free match line, got: "File: crlf.go\nMatch lines: 1\n3|func Crlf() {}\r\n\n"`.

Les deux tests exécutent de vraies commandes git dans un dépôt temporaire créé par
`t.TempDir()` ; l'identité git y est configurée localement par les fonctions
d'aide `setupFileFindRepo` et `setupTestRepo`, déjà présentes dans le projet.

### 6. Limites et effets de bord

**Le point le plus important : une partie de cette PR a été retirée après
relecture.**

La première version contenait une troisième correction. Elle faisait passer les
motifs d'exclusion de la ligne de commande (`--exclude`, dans
`cmd/opencodereview/shared.go`, fonction `applyCLIExcludes`) et les motifs
`include` / `exclude` de `rule.json` (dans `internal/config/rules/system_rules.go`,
fonction `buildFileFilter`) par `filepath.ToSlash`. L'intention était de permettre
à un utilisateur Windows d'écrire `src\gen\*` et que cela corresponde aux chemins
à barres obliques que git rapporte.

Le relecteur a signalé le problème, et il avait raison. Dans un motif glob,
l'antislash n'est pas seulement un séparateur de chemin : c'est aussi le caractère
d'échappement. Le motif `src/\[generated\]/file.go` sert à désigner un dossier
nommé littéralement `[generated]`, en neutralisant les crochets qui, sans
échappement, formeraient une classe de caractères. Le passer par `filepath.ToSlash`
sur Windows le transforme en `src/[generated]/file.go`, et le motif cesse de
correspondre. Autrement dit, la correction réparait un cas et en cassait un autre,
qui fonctionnait.

Plus profondément, `src\gen\*` est intrinsèquement ambigu sur Windows : rien dans
le motif ne dit si l'antislash sépare deux dossiers ou échappe l'astérisque. Il n'y
a pas de conversion automatique correcte ; il faut d'abord décider d'une règle.

La partie fautive a donc été retirée. La PR ne contient plus que les deux
corrections de sortie git décrites ci-dessus, et le traitement des motifs est
inchangé. La discussion de suivi sur la façon d'accepter les séparateurs
Windows se poursuit dans l'issue #1463.

Ce qu'il faut retenir pour défendre la PR : le périmètre actuel est délibérément
réduit, et les deux corrections restantes ne touchent à aucune logique de motifs.

**Autres limites :**

- **Aucun effet pour les utilisateurs dont git a déjà `core.quotepath=false`.**
  Pour eux, la sortie ne change pas.
- **`TrimSuffix` retire le `\r` final même s'il faisait partie du contenu réel.**
  Le cas suppose une ligne se terminant par un CR dans un fichier par ailleurs en
  LF, ce qui relève du fichier binaire plus que du code source. Le compromis me
  semble juste, mais il existe.
- **Les noms de fichiers contenant un saut de ligne ne sont pas traités.** Ils
  casseraient le découpage ligne par ligne de la sortie ; c'est un problème
  distinct, non abordé ici.
- **Le repli par parcours du système de fichiers** de `file_find`
  (`listWalkFiles`, utilisé quand le dossier n'est pas un dépôt git) n'était pas
  concerné : il ne passe pas par git, donc il n'a jamais échappé les noms.
- **Les tests dépendent de la présence de `git`** sur la machine. C'est déjà le
  cas de nombreux tests du paquet `internal/tool`.

### 7. Questions probables d'un mainteneur

**« Que s'est-il passé avec la conversion des antislashs de la première version ? »**
Elle était incorrecte et je l'ai retirée. Elle appliquait `filepath.ToSlash` aux
motifs d'exclusion, ce qui réécrit les échappements de glob : un motif valide
comme `src/\[generated\]/file.go` devenait `src/[generated]/file.go` et cessait
de correspondre. En plus, `src\gen\*` est ambigu sur Windows — l'antislash peut
être un séparateur ou un échappement — donc aucune conversion automatique ne peut
être juste dans tous les cas. Il faut d'abord décider de la règle, et c'est l'objet
de l'issue #1463.

**« Pourquoi deux corrections dans la même PR ? »**
Elles viennent du même constat : deux outils de l'agent lisent la sortie de git
sans la nettoyer comme le reste du projet le fait. Les deux sont de petites
modifications sur des fichiers voisins du même paquet, et chacune a son test. Si
vous préférez les séparer, c'est trivial.

**« Pourquoi passer `-c core.quotepath=false` à chaque appel plutôt que de le
définir une fois pour toutes ? »**
Parce que c'est ce que fait déjà le projet partout ailleurs, et parce qu'un
réglage global toucherait la configuration de l'utilisateur, ce qui n'est pas le
rôle d'un outil de revue. Le préfixe `-c` vaut pour une seule invocation. Un
répertoire d'aide qui centraliserait la construction des commandes git serait un
refactoring intéressant, mais il dépasse le cadre d'une correction de bug.

**« Pourquoi forcer `core.quotepath=true` dans le test ? Cela ne rend-il pas le
test artificiel ? »**
Au contraire, cela le rend fiable. `true` est la valeur par défaut de git, donc
c'est le comportement que rencontre l'utilisateur. Sans cette ligne, le test
passerait à tort sur une machine où l'option a été désactivée globalement, et le
bug pourrait revenir sans que personne ne le remarque.

**« Le `TrimSuffix` ne risque-t-il pas de supprimer un caractère significatif ? »**
Uniquement pour une ligne dont le dernier caractère réel serait un retour chariot,
dans un fichier qui n'est pas en CRLF. C'est un cas de fichier binaire ou très
inhabituel. J'ai choisi `TrimSuffix` plutôt qu'un remplacement global de tous les
`\r` précisément pour limiter l'impact au seul caractère de fin de ligne.

**« Y a-t-il d'autres appels git dans le code qui oublient `core.quotepath` ? »**
J'ai vérifié : après cette correction, tous les appels git qui lisent des chemins
passent l'option. On peut le contrôler avec un `grep` sur `core.quotepath` et une
recherche des invocations de `git` dans `internal/`. Un test de garde-fou au
niveau du paquet serait envisageable si vous voulez empêcher la régression à
l'avenir.

---

## PR #1434 — Écriture atomique de `config.json`

**Branche** : `fix/atomic-config-write` · **Fichiers touchés** :
`cmd/opencodereview/provider_cmd.go`, `cmd/opencodereview/provider_cmd_test.go`

### 1. Le problème en une phrase

`saveConfig` écrivait `~/.opencodereview/config.json` avec `os.WriteFile`, qui
vide le fichier avant d'y écrire : une interruption au mauvais moment laissait
une configuration tronquée ou vide, alors que ce fichier contient les clés d'API.

### 2. Pourquoi ça compte

`config.json` est le seul endroit où sont stockés le fournisseur, le modèle,
les URL, les serveurs MCP et surtout les clés d'API. Si le fichier est détruit,
l'utilisateur doit tout reconfigurer, et retrouver des clés qu'il n'a peut-être
plus.

La fenêtre de risque n'est pas théorique : `saveConfig` est appelée par toutes les
commandes `ocr config set`, `ocr config unset` et par l'assistant interactif de
configuration. Un `Ctrl+C` impatient, un disque plein, une coupure de courant, une
session SSH interrompue, et le fichier peut rester vide.

C'est aussi une question de qualité du code : le projet dispose déjà d'un écrivain
atomique pour les fichiers de sortie de revue (`lazyFileWriter`, dans
`cmd/opencodereview/shared.go`). Le fichier le plus sensible de l'outil n'en
bénéficiait pas.

### 3. Comment le bug se produit

Sur `main`, `saveConfig` (`cmd/opencodereview/provider_cmd.go:419`) faisait :

```go
if err := os.WriteFile(path, data, 0o600); err != nil { ... }
if err := os.Chmod(path, 0o600); err != nil { ... }
```

`os.WriteFile` ouvre le fichier avec les drapeaux
`O_WRONLY|O_CREATE|O_TRUNC`. Le `O_TRUNC` est le point critique : le fichier est
**ramené à zéro octet immédiatement**, avant la moindre écriture. La séquence sur
le disque est donc :

1. Le fichier existant, complet, avec ses clés.
2. Le fichier vide.
3. Le fichier avec le nouveau contenu.

Entre l'étape 2 et l'étape 3, si le processus est tué, si l'écriture échoue par
manque d'espace, ou si la machine s'arrête, le fichier reste vide ou partiel. Un
JSON coupé au milieu n'est pas analysable : au prochain lancement, l'outil ne
retrouve plus sa configuration.

Un détail secondaire explique la présence du `os.Chmod` : le mode passé à
`os.WriteFile` ne s'applique qu'à la **création** du fichier. Pour un fichier
existant, il est ignoré. Le `Chmod` était donc là pour forcer les permissions
`0600` (lecture et écriture par le propriétaire seulement) même sur un fichier
déjà présent.

### 4. Ce que fait la correction

`saveConfig` délègue à une nouvelle fonction `writeFileAtomic`
(`provider_cmd.go:436`). La création du dossier parent, faite plus haut par
`os.MkdirAll`, est inchangée. La séquence est :

1. **Résoudre la cible réelle** via `resolveSymlinkTarget` (voir plus bas).
2. **Créer un fichier temporaire dans le même dossier** que la cible :
   `os.CreateTemp(filepath.Dir(target), ".config-*.tmp")`. `os.CreateTemp` crée
   le fichier avec le mode `0600` dès l'origine : le secret n'est jamais, même
   brièvement, lisible par d'autres.
3. **Écrire** les données.
4. **`Sync()`** : demander au système d'écrire réellement les données sur le
   disque, au lieu de les laisser dans un tampon en mémoire. Sans cela, le
   renommage pourrait publier un fichier dont le contenu n'a pas encore atteint
   le support.
5. **`Close()`**, puis **`Chmod(0600)`** par sécurité explicite.
6. **`os.Rename(tmp, target)`** : remplacer la cible par le fichier temporaire.

**Pourquoi le renommage est la bonne opération.** Sur les systèmes POSIX,
renommer un fichier par-dessus un autre dans le même système de fichiers est
atomique : un lecteur voit soit entièrement l'ancien fichier, soit entièrement le
nouveau, jamais un état intermédiaire. Sous Windows, Go implémente `os.Rename`
avec `MoveFileEx` et le drapeau de remplacement, qui offre la même garantie de
substitution de l'entrée de répertoire.

**Pourquoi le même dossier.** Un renommage entre deux systèmes de fichiers
différents échoue (`EXDEV` sous Unix) et, quand une bibliothèque l'émule par une
copie, la garantie d'atomicité disparaît. Placer le fichier temporaire à côté de
la cible évite les deux problèmes. C'est aussi pour cela qu'on n'utilise pas le
dossier temporaire du système.

**Le nettoyage.** Chaque chemin d'échec appelle `cleanupOutputTemp`
(`cmd/opencodereview/shared.go:585`), une fonction d'aide déjà présente dans le
projet, qui ferme le descripteur et supprime le fichier temporaire. Les erreurs
sont combinées avec `errors.Join`, ce qui permet de signaler un échec de
nettoyage sans masquer la cause d'origine. Réutiliser cette fonction évite de
dupliquer la logique et garde le comportement cohérent avec l'écrivain de sortie
existant.

**La résolution des liens symboliques** (`resolveSymlinkTarget`,
`provider_cmd.go:471`) mérite une explication séparée.

Le problème : `os.WriteFile` suit les liens symboliques. Si
`~/.opencodereview/config.json` est un lien vers
`~/dotfiles/ocr/config.json`, l'écriture modifie le fichier de destination et le
lien reste un lien. Un renommage, lui, **remplacerait le lien par un fichier
ordinaire**, ce qui casserait la mise en place de l'utilisateur. Il faut donc
renommer par-dessus la cible finale, pas par-dessus le lien.

La fonction suit la chaîne de liens à la main :

- `os.Lstat` inspecte le chemin **sans** suivre le lien ;
- si le chemin n'existe pas, on le retourne tel quel (on le créera) ;
- si ce n'est pas un lien, on le retourne : c'est la cible ;
- sinon, `os.Readlink` donne la destination ; un chemin relatif est rendu absolu
  par rapport au dossier du lien, et on recommence.

La boucle est bornée par `maxSymlinkHops = 40` (`provider_cmd.go:465`), valeur
qui correspond à la limite `ELOOP` courante des systèmes Unix. Au-delà, on
retourne une erreur : cela protège contre deux liens qui pointent l'un vers
l'autre, qui feraient sinon boucler le programme indéfiniment.

**Pourquoi ne pas utiliser `filepath.EvalSymlinks`.** C'est la fonction évidente,
et c'est précisément le piège : elle exige que **tous** les éléments du chemin,
y compris la cible finale, existent déjà. Un lien vers un fichier pas encore créé
— par exemple un dépôt de dotfiles fraîchement cloné où le `config.json` n'a
jamais été écrit — la fait échouer. Or `os.WriteFile` créait ce fichier sans
broncher. Utiliser `EvalSymlinks` aurait donc introduit une régression. C'est
exactement ce qui est arrivé (voir la section Limites).

### 5. La preuve

Cinq tests ajoutés dans `cmd/opencodereview/provider_cmd_test.go`.

**`TestSaveConfig_ReplacesWithoutLeavingTempFiles`.** Cas nominal : on part d'un
`config.json` existant, on enregistre une nouvelle configuration, on vérifie que
le contenu a bien été remplacé **et** que le dossier ne contient qu'une seule
entrée. La seconde assertion est la plus utile : elle détecte un fichier
`.config-*.tmp` oublié, l'erreur classique de ce genre d'implémentation.

**`TestSaveConfig_FailedReplaceCleansUpTemp`.** On place un **dossier** à
l'emplacement de `config.json`. Le renommage final échoue forcément (on ne peut
pas renommer un fichier par-dessus un dossier). Le test exige une erreur, et
exige que le dossier parent ne contienne toujours qu'une seule entrée : le
fichier temporaire a donc bien été supprimé malgré l'échec. C'est une façon
simple et portable de provoquer une panne sur le dernier chemin.

**`TestSaveConfig_WritesThroughSymlink`.** Un lien `config.json` vers un
`real.json` existant. Après l'enregistrement, deux vérifications : `config.json`
est **toujours** un lien symbolique (`os.Lstat` + test du bit `ModeSymlink`), et
`real.json` contient bien le nouveau contenu. C'est le test qui prouve qu'on n'a
pas cassé la configuration en dotfiles.

**`TestSaveConfig_WritesThroughSymlinkToMissingTarget`.** Un lien **relatif**
(`real.json`) vers un fichier qui n'existe pas encore. L'enregistrement doit
réussir et créer la cible. C'est ce test qui échoue sur la première version de la
PR ; il a été ajouté en même temps que la correction.

**`TestResolveSymlinkTarget_Loop`.** Deux liens `a` et `b` qui pointent l'un vers
l'autre. La fonction doit retourner une erreur, pas boucler. Sans ce test, la
borne de 40 sauts ne serait vérifiée par rien.

Les trois tests avec liens symboliques appellent `t.Skipf` si la création d'un
lien échoue : sous Windows, elle demande le mode développeur ou des privilèges
particuliers. Les tests sont donc ignorés proprement au lieu d'échouer sur une
machine mal configurée.

### 6. Limites et effets de bord

**Le point à annoncer : la première version cassait un cas qui fonctionnait.**

Elle utilisait `resolveOutputCommitPath`, une fonction existante du projet qui
s'appuie sur `filepath.EvalSymlinks`. Cette fonction exige que la cible du lien
existe déjà. Conséquence : un `config.json` qui était un lien symbolique vers un
fichier pas encore créé — cas courant avec un dépôt de dotfiles fraîchement
cloné — provoquait une erreur, alors que `os.WriteFile` créait simplement le
fichier. C'était une régression introduite par la correction elle-même.

Le problème a été repéré sur cette première version et corrigé par l'écriture de
`resolveSymlinkTarget`, qui suit la chaîne de liens sans exiger que la cible
existe, et le test `TestSaveConfig_WritesThroughSymlinkToMissingTarget` a été
ajouté pour verrouiller ce comportement. Il échoue sur la version précédente et
passe sur la version actuelle.

La leçon, qu'il faut assumer : remplacer une écriture directe par un renommage
change la façon dont les liens symboliques sont traités, et il fallait examiner
ce cas dès le départ.

**Autres limites :**

- **Le scénario de coupure n'est pas simulé dans les tests.** Les tests couvrent
  le nouveau chemin de code — remplacement, nettoyage, liens, boucles — mais
  personne ne tue le processus au milieu d'une écriture. La garantie repose sur
  l'atomicité du renommage, qui est une propriété du système de fichiers, pas du
  code de la PR.
- **Le dossier parent n'est pas synchronisé après le renommage.** Sur certains
  systèmes de fichiers, après une coupure de courant, le renommage lui-même peut
  ne pas être durable. Le contenu n'est jamais corrompu — on retrouve soit
  l'ancien fichier, soit le nouveau — mais le tout dernier enregistrement peut
  être perdu. Ajouter un `fsync` sur le répertoire est possible ; ce n'est pas
  portable sous Windows et cela demanderait une écriture conditionnelle par
  plateforme.
- **Un fichier temporaire peut subsister.** Si le processus est tué entre la
  création du temporaire et le renommage, un `.config-*.tmp` reste dans le
  dossier. Il ne gêne rien, mais il n'est pas nettoyé automatiquement au
  lancement suivant.
- **Les liens physiques (hard links) sont rompus.** Si `config.json` partageait
  son inode avec un autre chemin, le renommage crée une nouvelle entrée et le
  partage disparaît. `os.WriteFile` le préservait. C'est un cas rare, mais réel.
- **Les attributs propres au fichier cible sont perdus.** Un mode personnalisé,
  des attributs étendus ou une ACL posés sur `config.json` ne survivent pas au
  remplacement : le nouveau fichier porte le mode `0600` et les attributs du
  temporaire. Comme `0600` était déjà appliqué explicitement avant, c'est sans
  conséquence dans le cas normal.
- **Le dossier doit être accessible en écriture** — il l'était déjà — et doit
  disposer brièvement de la place pour une seconde copie du fichier. Le fichier
  fait quelques kilo-octets.
- **Sous Windows, le renommage échoue si un autre processus tient le fichier
  ouvert.** `os.WriteFile` avait une contrainte comparable, mais le message
  d'erreur change.
- **La résolution de lien n'est pas protégée contre une course.** Entre le moment
  où la cible est résolue et le renommage, le lien pourrait être modifié par un
  autre processus. Dans un dossier appartenant à l'utilisateur, le risque est
  faible, et il existait déjà avec `os.WriteFile`.

### 7. Questions probables d'un mainteneur

**« Que s'est-il passé avec la première version et les liens symboliques ? »**
Elle réutilisait `resolveOutputCommitPath`, qui s'appuie sur
`filepath.EvalSymlinks`. Cette fonction exige que la cible du lien existe déjà,
si bien qu'un `config.json` pointant vers un fichier pas encore créé — un dépôt
de dotfiles fraîchement cloné, par exemple — échouait, alors que l'ancien code le
créait sans problème. J'ai écrit `resolveSymlinkTarget`, qui suit la chaîne de
liens sans cette exigence, et ajouté un test dédié qui échoue sur l'ancienne
version.

**« Pourquoi ne pas réutiliser `lazyFileWriter`, qui fait déjà une écriture
atomique dans ce projet ? »**
`lazyFileWriter` est conçu pour un flux de sortie de revue : il diffère la
création du fichier jusqu'à la première écriture et implémente `io.Writer`. Ici,
on a un tableau d'octets complet et une seule écriture, donc l'interface ne
correspond pas. En revanche, j'ai bien réutilisé `cleanupOutputTemp`, la fonction
de nettoyage partagée, pour ne pas dupliquer la gestion des erreurs.

**« Le `Sync()` est-il nécessaire ? Il coûte cher. »**
Il l'est pour la garantie visée. Sans `Sync`, le renommage peut publier une entrée
de répertoire qui pointe vers un contenu encore en mémoire ; après une coupure, on
retrouverait un fichier de la bonne taille au contenu indéterminé. Le coût est un
appel système par enregistrement de configuration, c'est-à-dire lors d'un
`ocr config set` — une opération manuelle et rare, pas un chemin chaud.

**« Pourquoi 40 sauts de lien symbolique ? »**
C'est la limite classique `ELOOP` de Linux et de la plupart des systèmes Unix,
donc une chaîne plus longue ne serait de toute façon pas résoluble par le système
lui-même. Le but n'est pas de fixer une limite fine, seulement d'éviter une boucle
infinie sur deux liens qui se pointent mutuellement. Le test
`TestResolveSymlinkTarget_Loop` couvre ce cas.

**« Pourquoi refaire un `Chmod` alors que `os.CreateTemp` crée déjà le fichier en
0600 ? »**
C'est une ceinture et des bretelles, à coût nul. `os.CreateTemp` crée en `0600`,
donc l'appel est normalement redondant. Je l'ai gardé pour que l'intention reste
explicite dans le code : ce fichier contient des secrets et doit finir en `0600`,
quoi qu'il arrive. Je peux le retirer si vous préférez un code plus court.

**« Les tests ne prouvent pas que la coupure au milieu de l'écriture est
réellement évitée. »**
C'est exact, et je le dis dans la description de la PR. Simuler une coupure de
courant dans un test unitaire n'est pas réaliste. Les tests prouvent que le
nouveau chemin de code fonctionne : remplacement effectif, aucun temporaire
oublié, nettoyage en cas d'échec, liens symboliques respectés dans les deux cas,
boucles détectées. La garantie d'atomicité elle-même vient du renommage, qui est
une propriété du système de fichiers.

**« Le renommage casse les liens physiques et peut perdre des attributs
personnalisés. Est-ce acceptable ? »**
C'est le compromis habituel de l'écriture atomique, et je pense qu'il est bon ici :
on échange un cas rare — un `config.json` lié physiquement ailleurs ou porteur
d'attributs particuliers — contre la protection d'un fichier qui contient des clés
d'API. Les liens symboliques, eux, qui sont le cas d'usage réellement répandu avec
les dotfiles, sont préservés et couverts par deux tests.
