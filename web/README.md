# web

Next.js frontend for namma-transit. See the repository README for the full
picture.

```sh
cp .env.example .env.local   # NEXT_PUBLIC_API=http://localhost:8080
npm install
npm run dev
```

Generated protobuf/Connect types live in `src/gen` and come from
`buf generate` at the repository root; do not edit them by hand.
