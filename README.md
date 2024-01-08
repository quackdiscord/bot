# 🌱 Seeds

A Discord bot focused on making Discord safer. Providing exceptional tools for moderation and community protection.

All Seeds' commands use Discord's
[slash command interface](https://discord.com/developers/docs/interactions/application-commands#slash-commands)

[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![GitHub issues](https://img.shields.io/github/issues/seedsdiscord/bot)](https://github.com/seedsdiscord/bot/issues)
[![GitHub forks](https://img.shields.io/github/forks/seedsdiscord/bot)](https://github.com/seedsdiscord/bot/network)
[![GitHub stars](https://img.shields.io/github/stars/seedsdiscord/bot)](https://github.com/seedsdiscord/bot/stargazers)

## Running Locally

Seeds uses the [Bun](https://bun.sh) runtime. Please ensure you have Bun installed before continuing. Please also ensure
you have created a bot via [Discord's Developer Portal](https://discord.com/developers/applications).

1. Clone, and install dependencies.

```
mkdir seeds && cd seeds
git clone https://github.com/seedsdiscord/bot
cd bot
bun i
```

2. Set up enviornment variables.

-   Create a `.env` in the root of the project.
-   Refer to [`config.ts`](./config.ts) to see all the required enviornment variables.
-   Feel free to omit any for testing.

3. Start the bot!

```
bun dev
```

-   This wil run the dev script.

## Contributing

1. Fork the repository
2. Create a new branch: `git checkout -b feature/new-feature`
3. Make your changes and commit them: `git commit -m 'Add new feature'`
4. Push to the branch: `git push origin feature/new-feature`
5. Open a pull request

## License

This project is licensed under the MIT License - see the [LICENSE](./LICENSE) file for details.
