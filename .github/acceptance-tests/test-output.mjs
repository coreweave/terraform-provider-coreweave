import { once } from 'node:events';
import { createInterface } from 'node:readline';

const lines = createInterface({ input: process.stdin, crlfDelay: Infinity });
for await (const line of lines) {
  const event = JSON.parse(line);
  if (typeof event.Output === 'string' && !process.stdout.write(event.Output)) {
    await once(process.stdout, 'drain');
  }
}
