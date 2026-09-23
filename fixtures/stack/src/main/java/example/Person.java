package example;

import jakarta.persistence.Entity;
import jakarta.persistence.Id;
import lombok.Getter;
import lombok.Setter;
import lombok.NoArgsConstructor;
import lombok.AllArgsConstructor;
import lombok.Builder;

@Entity
@Getter @Setter @NoArgsConstructor @AllArgsConstructor @Builder
public class Person {
    @Id private Long id;
    private String name;
}
