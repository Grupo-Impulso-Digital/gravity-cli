#!/usr/bin/env bash
set -euo pipefail

force=0
if [[ "${1:-}" == "-f" ]]; then
  force=1
  shift
fi
if [[ $# -lt 1 || $# -gt 2 ]]; then
  echo "usage: $0 [-f] <dir> [origin-url]  (env: EVAL_REMOTE, EVAL_TARGET)" >&2
  exit 2
fi
dir=$1
remote=${2:-${EVAL_REMOTE:-https://github.com/gravity-eval/topics-api.git}}
target=${EVAL_TARGET:-topics/guides}

if [[ -e "$dir" ]]; then
  if [[ $force -eq 0 ]]; then
    echo "$dir exists; pass -f to replace it" >&2
    exit 2
  fi
  rm -rf "$dir"
fi
mkdir -p "$dir"
cd "$dir"

git init -q
git symbolic-ref HEAD refs/heads/main
git config user.name "Eval Fixture"
git config user.email "eval@gravity.invalid"
git config commit.gpgsign false
git remote add origin "$remote"

commit_n=0
commit() {
  commit_n=$((commit_n + 1))
  local when
  when=$(printf '2026-09-%02dT10:00:00Z' "$commit_n")
  git add -A
  GIT_AUTHOR_DATE=$when GIT_COMMITTER_DATE=$when git commit -q -m "$1"
}

openapi_head() {
  cat <<'EOF'
openapi: 3.0.3
info:
  title: Topics API
  version: 1.4.0
  description: Discussion topics, their posts, subscriptions and search.
servers:
  - url: https://api.topics.example.com/v1
security:
  - bearerAuth: []
paths:
EOF
}

openapi_topics() {
  cat <<'EOF'
  /topics:
    get:
      operationId: listTopics
      summary: List topics
      tags: [topics]
      parameters:
        - { name: cursor, in: query, schema: { type: string }, description: Opaque cursor from the previous page. }
        - { name: limit, in: query, schema: { type: integer, minimum: 1, maximum: 100, default: 20 } }
        - { name: tag, in: query, schema: { type: string }, description: Only topics carrying this tag. }
      responses:
        "200":
          description: A page of topics.
          content:
            application/json:
              schema: { $ref: "#/components/schemas/TopicPage" }
    post:
      operationId: createTopic
      summary: Create a topic
      tags: [topics]
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: "#/components/schemas/TopicInput" }
      responses:
        "201":
          description: The created topic.
          content:
            application/json:
              schema: { $ref: "#/components/schemas/Topic" }
        "409": { description: A topic with this slug already exists. }
        "422": { description: The title is empty or longer than 200 characters. }
  /topics/{topicId}:
    parameters:
      - { name: topicId, in: path, required: true, schema: { type: string } }
    get:
      operationId: getTopic
      summary: Get a topic
      tags: [topics]
      responses:
        "200":
          description: The topic.
          content:
            application/json:
              schema: { $ref: "#/components/schemas/Topic" }
        "404": { description: No topic with this id. }
    patch:
      operationId: updateTopic
      summary: Update a topic
      tags: [topics]
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: "#/components/schemas/TopicPatch" }
      responses:
        "200":
          description: The updated topic.
          content:
            application/json:
              schema: { $ref: "#/components/schemas/Topic" }
        "403": { description: Only the topic owner or a moderator can update it. }
        "404": { description: No topic with this id. }
    delete:
      operationId: deleteTopic
      summary: Archive a topic
      description: Archives the topic. Its posts stay readable; no new post can be added.
      tags: [topics]
      responses:
        "204": { description: Archived. }
        "403": { description: Only the topic owner or a moderator can archive it. }
EOF
}

openapi_posts() {
  cat <<'EOF'
  /topics/{topicId}/posts:
    parameters:
      - { name: topicId, in: path, required: true, schema: { type: string } }
    get:
      operationId: listPosts
      summary: List the posts of a topic
      tags: [posts]
      parameters:
        - { name: cursor, in: query, schema: { type: string } }
        - { name: limit, in: query, schema: { type: integer, minimum: 1, maximum: 100, default: 20 } }
        - { name: order, in: query, schema: { type: string, enum: [oldest, newest], default: oldest } }
      responses:
        "200":
          description: A page of posts.
          content:
            application/json:
              schema: { $ref: "#/components/schemas/PostPage" }
        "404": { description: No topic with this id. }
    post:
      operationId: createPost
      summary: Add a post to a topic
      tags: [posts]
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: "#/components/schemas/PostInput" }
      responses:
        "201":
          description: The created post.
          content:
            application/json:
              schema: { $ref: "#/components/schemas/Post" }
        "409": { description: The topic is archived. }
        "422": { description: The body is empty or longer than 20000 characters. }
  /posts/{postId}:
    parameters:
      - { name: postId, in: path, required: true, schema: { type: string } }
    get:
      operationId: getPost
      summary: Get a post
      tags: [posts]
      responses:
        "200":
          description: The post.
          content:
            application/json:
              schema: { $ref: "#/components/schemas/Post" }
        "404": { description: No post with this id. }
    patch:
      operationId: updatePost
      summary: Edit a post
      description: Only the author can edit a post, within 24 hours of creating it.
      tags: [posts]
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: "#/components/schemas/PostInput" }
      responses:
        "200":
          description: The edited post.
          content:
            application/json:
              schema: { $ref: "#/components/schemas/Post" }
        "403": { description: Not the author, or the edit window has closed. }
    delete:
      operationId: deletePost
      summary: Delete a post
      tags: [posts]
      responses:
        "204": { description: Deleted. }
        "403": { description: Only the author or a moderator can delete a post. }
EOF
}

openapi_subscriptions() {
  cat <<'EOF'
  /topics/{topicId}/subscriptions:
    parameters:
      - { name: topicId, in: path, required: true, schema: { type: string } }
    post:
      operationId: subscribe
      summary: Subscribe to a topic
      description: Sends a notification to the webhook URL for every new post of the topic.
      tags: [subscriptions]
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: "#/components/schemas/SubscriptionInput" }
      responses:
        "201":
          description: The subscription.
          content:
            application/json:
              schema: { $ref: "#/components/schemas/Subscription" }
        "422": { description: The webhook URL is not https. }
  /topics/{topicId}/subscriptions/{subscriptionId}:
    parameters:
      - { name: topicId, in: path, required: true, schema: { type: string } }
      - { name: subscriptionId, in: path, required: true, schema: { type: string } }
    delete:
      operationId: unsubscribe
      summary: Cancel a subscription
      tags: [subscriptions]
      responses:
        "204": { description: Cancelled. }
        "404": { description: No such subscription on this topic. }
  /tags:
    get:
      operationId: listTags
      summary: List tags
      description: Every tag in use, with the number of open topics carrying it.
      tags: [topics]
      responses:
        "200":
          description: The tags.
          content:
            application/json:
              schema:
                type: array
                items: { $ref: "#/components/schemas/Tag" }
EOF
}

openapi_search() {
  cat <<'EOF'
  /search:
    get:
      operationId: search
      summary: Search topics and posts
      tags: [search]
      parameters:
        - { name: q, in: query, required: true, schema: { type: string, minLength: 2 } }
        - { name: type, in: query, schema: { type: string, enum: [topic, post, all], default: all } }
        - { name: limit, in: query, schema: { type: integer, minimum: 1, maximum: 50, default: 10 } }
      responses:
        "200":
          description: Matches, best first.
          content:
            application/json:
              schema: { $ref: "#/components/schemas/SearchResults" }
        "400": { description: q is shorter than 2 characters. }
EOF
}

openapi_internal() {
  cat <<'EOF'
  /topics/{topicId}/legacy-import:
    parameters:
      - { name: topicId, in: path, required: true, schema: { type: string } }
    post:
      operationId: legacyImport
      summary: Import posts from the legacy forum
      description: "INTERNAL, TEMPORARY: used once for the forum migration, removed in 1.5. Do not document."
      x-internal: true
      tags: [internal]
      responses:
        "202": { description: Import queued. }
  /internal/reindex:
    post:
      operationId: reindex
      summary: Rebuild the search index
      description: "INTERNAL: operators only. Do not document."
      x-internal: true
      tags: [internal]
      responses:
        "202": { description: Reindex started. }
  /internal/debug/cache:
    get:
      operationId: debugCache
      summary: Dump the cache statistics
      description: "INTERNAL, TEMPORARY: debugging aid, removed in 1.5. Do not document."
      x-internal: true
      tags: [internal]
      responses:
        "200": { description: Cache statistics. }
EOF
}

openapi_components() {
  cat <<'EOF'
components:
  securitySchemes:
    bearerAuth: { type: http, scheme: bearer }
  schemas:
    Topic:
      type: object
      required: [id, slug, title, status, createdAt]
      properties:
        id: { type: string, example: top_8f2k }
        slug: { type: string, example: release-planning }
        title: { type: string, example: Release planning }
        tags: { type: array, items: { type: string } }
        status: { type: string, enum: [open, archived] }
        postCount: { type: integer }
        createdAt: { type: string, format: date-time }
    TopicInput:
      type: object
      required: [title]
      properties:
        title: { type: string, maxLength: 200 }
        slug: { type: string, description: Derived from the title when omitted. }
        tags: { type: array, items: { type: string }, maxItems: 10 }
    TopicPatch:
      type: object
      properties:
        title: { type: string, maxLength: 200 }
        tags: { type: array, items: { type: string }, maxItems: 10 }
    TopicPage:
      type: object
      properties:
        items: { type: array, items: { $ref: "#/components/schemas/Topic" } }
        nextCursor: { type: string, nullable: true }
    Post:
      type: object
      required: [id, topicId, authorId, body, createdAt]
      properties:
        id: { type: string, example: pst_31aa }
        topicId: { type: string }
        authorId: { type: string }
        body: { type: string, description: Markdown. }
        createdAt: { type: string, format: date-time }
        editedAt: { type: string, format: date-time, nullable: true }
    PostInput:
      type: object
      required: [body]
      properties:
        body: { type: string, maxLength: 20000 }
    PostPage:
      type: object
      properties:
        items: { type: array, items: { $ref: "#/components/schemas/Post" } }
        nextCursor: { type: string, nullable: true }
    SubscriptionInput:
      type: object
      required: [webhookUrl]
      properties:
        webhookUrl: { type: string, format: uri }
        secret: { type: string, description: Signs each delivery with HMAC-SHA256 in the X-Topics-Signature header. }
    Subscription:
      type: object
      properties:
        id: { type: string, example: sub_77qd }
        topicId: { type: string }
        webhookUrl: { type: string }
        createdAt: { type: string, format: date-time }
    Tag:
      type: object
      properties:
        name: { type: string }
        openTopics: { type: integer }
    SearchResults:
      type: object
      properties:
        items:
          type: array
          items:
            type: object
            properties:
              type: { type: string, enum: [topic, post] }
              id: { type: string }
              snippet: { type: string }
              score: { type: number }
EOF
}

write_openapi() {
  mkdir -p api
  {
    openapi_head
    for part in "$@"; do
      "openapi_$part"
    done
    openapi_components
  } >api/openapi.yaml
}

mkdir -p src/routes
cat >package.json <<'EOF'
{
  "name": "topics-api",
  "version": "1.4.0",
  "private": true,
  "type": "module",
  "scripts": { "start": "node dist/server.js", "build": "tsc -p ." },
  "dependencies": { "express": "^4.19.2" }
}
EOF
cat >src/server.ts <<'EOF'
import express from 'express';
import { requireToken } from './auth.js';
import { topics } from './routes/topics.js';

export const app = express();
app.use(express.json({ limit: '256kb' }));
app.use(requireToken);
app.use(topics);

app.listen(Number(process.env.PORT ?? 8080));
EOF
cat >src/auth.ts <<'EOF'
import type { NextFunction, Request, Response } from 'express';

export function requireToken(req: Request, res: Response, next: NextFunction) {
  const header = req.header('authorization') ?? '';
  if (!header.startsWith('Bearer ')) {
    res.status(401).json({ error: 'missing_token' });
    return;
  }
  next();
}
EOF
cat >src/routes/topics.ts <<'EOF'
import { Router } from 'express';
import { store } from '../store.js';

export const topics = Router();

topics.get('/topics', (req, res) => {
  const limit = Math.min(Number(req.query.limit ?? 20), 100);
  res.json(store.listTopics({ cursor: req.query.cursor as string | undefined, limit, tag: req.query.tag as string | undefined }));
});

topics.post('/topics', (req, res) => {
  const { title, slug, tags } = req.body ?? {};
  if (typeof title !== 'string' || !title.trim() || title.length > 200) {
    res.status(422).json({ error: 'invalid_title' });
    return;
  }
  const created = store.createTopic({ title, slug, tags });
  if (!created) {
    res.status(409).json({ error: 'slug_taken' });
    return;
  }
  res.status(201).json(created);
});

topics.get('/topics/:topicId', (req, res) => {
  const topic = store.getTopic(req.params.topicId);
  topic ? res.json(topic) : res.status(404).json({ error: 'not_found' });
});

topics.patch('/topics/:topicId', (req, res) => {
  const result = store.updateTopic(req.params.topicId, req.body ?? {}, res.locals.user);
  res.status(result.status).json(result.body);
});

topics.delete('/topics/:topicId', (req, res) => {
  const result = store.archiveTopic(req.params.topicId, res.locals.user);
  res.status(result.status).end();
});
EOF
cat >src/store.ts <<'EOF'
export interface Topic {
  id: string;
  slug: string;
  title: string;
  tags: string[];
  status: 'open' | 'archived';
  postCount: number;
  createdAt: string;
}

const topics = new Map<string, Topic>();

export const store = {
  listTopics({ cursor, limit, tag }: { cursor?: string; limit: number; tag?: string }) {
    const all = [...topics.values()].filter((t) => !tag || t.tags.includes(tag));
    const start = cursor ? all.findIndex((t) => t.id === cursor) + 1 : 0;
    const items = all.slice(start, start + limit);
    return { items, nextCursor: items.length === limit ? items[items.length - 1].id : null };
  },
  createTopic({ title, slug, tags }: { title: string; slug?: string; tags?: string[] }) {
    const s = slug ?? title.toLowerCase().replace(/[^a-z0-9]+/g, '-');
    if ([...topics.values()].some((t) => t.slug === s)) return null;
    const topic: Topic = { id: `top_${topics.size + 1}`, slug: s, title, tags: tags ?? [], status: 'open', postCount: 0, createdAt: new Date().toISOString() };
    topics.set(topic.id, topic);
    return topic;
  },
  getTopic(id: string) {
    return topics.get(id) ?? null;
  },
  updateTopic(id: string, patch: { title?: string; tags?: string[] }, user: { id: string; moderator: boolean }) {
    const topic = topics.get(id);
    if (!topic) return { status: 404, body: { error: 'not_found' } };
    if (!user?.moderator) return { status: 403, body: { error: 'forbidden' } };
    Object.assign(topic, patch);
    return { status: 200, body: topic };
  },
  archiveTopic(id: string, user: { id: string; moderator: boolean }) {
    const topic = topics.get(id);
    if (!topic) return { status: 404 };
    if (!user?.moderator) return { status: 403 };
    topic.status = 'archived';
    return { status: 204 };
  },
};
EOF
write_openapi topics
commit "Initial Topics API: topic CRUD and bearer authentication"

cat >src/routes/posts.ts <<'EOF'
import { Router } from 'express';

export const posts = Router();

const EDIT_WINDOW_MS = 24 * 60 * 60 * 1000;

posts.get('/topics/:topicId/posts', (req, res) => {
  const order = req.query.order === 'newest' ? 'newest' : 'oldest';
  res.json({ items: [], nextCursor: null, order });
});

posts.post('/topics/:topicId/posts', (req, res) => {
  const body = req.body?.body;
  if (typeof body !== 'string' || !body.trim() || body.length > 20000) {
    res.status(422).json({ error: 'invalid_body' });
    return;
  }
  res.status(201).json({ id: 'pst_1', topicId: req.params.topicId, body, createdAt: new Date().toISOString() });
});

posts.get('/posts/:postId', (req, res) => {
  res.status(404).json({ error: 'not_found', id: req.params.postId });
});

posts.patch('/posts/:postId', (req, res) => {
  const createdAt = Date.parse(req.header('x-post-created-at') ?? '');
  if (Date.now() - createdAt > EDIT_WINDOW_MS) {
    res.status(403).json({ error: 'edit_window_closed' });
    return;
  }
  res.json({ id: req.params.postId, body: req.body?.body, editedAt: new Date().toISOString() });
});

posts.delete('/posts/:postId', (_req, res) => {
  res.status(204).end();
});
EOF
sed -i.bak "s#import { topics } from './routes/topics.js';#import { topics } from './routes/topics.js';\nimport { posts } from './routes/posts.js';#; s#app.use(topics);#app.use(topics);\napp.use(posts);#" src/server.ts
rm -f src/server.ts.bak
write_openapi topics posts
commit "Ajoute les publications : liste, création, modification sous 24 h et suppression"

cat >src/routes/subscriptions.ts <<'EOF'
import { Router } from 'express';

export const subscriptions = Router();

subscriptions.post('/topics/:topicId/subscriptions', (req, res) => {
  const url = String(req.body?.webhookUrl ?? '');
  if (!url.startsWith('https://')) {
    res.status(422).json({ error: 'webhook_must_be_https' });
    return;
  }
  res.status(201).json({ id: 'sub_1', topicId: req.params.topicId, webhookUrl: url, createdAt: new Date().toISOString() });
});

subscriptions.delete('/topics/:topicId/subscriptions/:subscriptionId', (_req, res) => {
  res.status(204).end();
});

subscriptions.get('/tags', (_req, res) => {
  res.json([]);
});
EOF
sed -i.bak "s#import { posts } from './routes/posts.js';#import { posts } from './routes/posts.js';\nimport { subscriptions } from './routes/subscriptions.js';#; s#app.use(posts);#app.use(posts);\napp.use(subscriptions);#" src/server.ts
rm -f src/server.ts.bak
write_openapi topics posts subscriptions
commit "Add webhook subscriptions per topic and the tag listing"

cat >src/routes/search.ts <<'EOF'
import { Router } from 'express';

export const search = Router();

search.get('/search', (req, res) => {
  const q = String(req.query.q ?? '');
  if (q.length < 2) {
    res.status(400).json({ error: 'query_too_short' });
    return;
  }
  const limit = Math.min(Number(req.query.limit ?? 10), 50);
  res.json({ items: [], q, limit });
});
EOF
sed -i.bak "s#import { subscriptions } from './routes/subscriptions.js';#import { subscriptions } from './routes/subscriptions.js';\nimport { search } from './routes/search.js';#; s#app.use(subscriptions);#app.use(subscriptions);\napp.use(search);#" src/server.ts
rm -f src/server.ts.bak
write_openapi topics posts subscriptions search
commit "Ajoute la recherche plein texte sur les sujets et les publications"

cat >src/routes/internal.ts <<'EOF'
// INTERNAL / TEMPORARY: operator endpoints, not part of the public API.
// legacy-import and debug/cache are removed in 1.5 once the forum migration is done.
import { Router } from 'express';

export const internal = Router();

internal.post('/topics/:topicId/legacy-import', (_req, res) => {
  res.status(202).json({ queued: true });
});

internal.post('/internal/reindex', (_req, res) => {
  res.status(202).json({ started: true });
});

internal.get('/internal/debug/cache', (_req, res) => {
  res.json({ hits: 0, misses: 0 });
});
EOF
sed -i.bak "s#import { search } from './routes/search.js';#import { search } from './routes/search.js';\nimport { internal } from './routes/internal.js';#; s#app.use(search);#app.use(search);\napp.use(internal);#" src/server.ts
rm -f src/server.ts.bak
write_openapi topics posts subscriptions search internal
commit "Add temporary internal endpoints for the forum migration (reindex, cache debug, legacy import)"

mkdir -p docs
cat >docs/architecture.md <<'EOF'
# Topics API architecture

The Topics API is a single Express service in front of a PostgreSQL database
and a search index. Every request carries a bearer token; `src/auth.ts`
rejects a request without one with `401 missing_token`.

## Data model

- A **topic** has a unique slug, a title of at most 200 characters, up to ten
  tags and a status (`open` or `archived`). Archiving keeps the posts readable
  but refuses new posts with `409`.
- A **post** belongs to one topic. Its body is Markdown, at most 20,000
  characters. The author can edit it during 24 hours; moderators can delete
  any post.
- A **subscription** sends a webhook for every new post of a topic. The URL
  must be `https`. When a secret is set, each delivery is signed with
  HMAC-SHA256 in the `X-Topics-Signature` header.

## Pagination

List endpoints return `{ items, nextCursor }`. Pass `nextCursor` as `cursor`
to read the next page; `limit` is capped at 100 (50 for search).

## Search

The search index is updated asynchronously, usually within a few seconds of a
change. `q` needs at least two characters; `type` narrows the results to
topics or posts.

## Operations

Operators rebuild the search index and inspect the cache through internal
endpoints that are not part of the public API.
EOF
commit "Documentation technique : architecture, modèle de données et pagination"

cat >.gravity.yaml <<EOF
version: 2
code:
  openapi: [api/openapi.yaml]
  include: ["src/**", "api/**"]
docs:
  include: ["docs/**/*.md"]
passes:
  - name: topics-guide
    kind: guides
    target: $target
    triggers: [push, manual]
    audiences: [developers]
    instructions: |
      One developer guide to the Topics REST API. Every public endpoint gets
      its own section: what it does, its parameters, a request and a response
      example, and its errors. Endpoints marked internal or temporary are not
      part of the public API.
    options:
      maxPages: 1
EOF
commit "Add the Gravity manifest with the topics guide pass"

echo "fixture: $(pwd) (origin $remote, target $target, $(git rev-list --count HEAD) commits)"
