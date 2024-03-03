import { FastifyInstance } from "fastify";

export default async (sv: FastifyInstance, opts: any) => {
	sv.all("/print/hello", async function handler(request, reply) {
		return { hello: "world", body: request.body };
	});
};
