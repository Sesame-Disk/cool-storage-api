CREATE DATABASE sample_db DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
CREATE USER 'sample_db_user'@'%' IDENTIFIED WITH mysql_native_password BY 'EXAMPLE_PASSWORD';
GRANT ALL PRIVILEGES ON sample_db.* TO 'sample_db_user'@'%';
FLUSH PRIVILEGES;


USE sample_db;

CREATE TABLE system_users (
       user_id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
       username VARCHAR(50),
       password VARCHAR(255)
) ENGINE = InnoDB;


CREATE TABLE authentication_tokens (
       token_id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
       user_id BIGINT,
       auth_token VARCHAR(255),
       generated_at DATETIME,
       expires_at   DATETIME
) ENGINE = InnoDB;

QUIT;
